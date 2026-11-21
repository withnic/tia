package tia

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Defaults for ScanOptions.
const (
	DefaultIntegrationTag    = "integration"
	DefaultIntegrationSuffix = "_integration_test.go"
)

// ScanOptions configures Scan.
type ScanOptions struct {
	// Root is the module root directory (the directory containing go.mod).
	Root string
	// Tag is the build tag marking integration test files. Empty means
	// DefaultIntegrationTag.
	Tag string
	// Pattern is the file name suffix marking integration test files. Empty
	// means DefaultIntegrationSuffix.
	Pattern string
	// HubPatterns are path.Match patterns matched against function and
	// method base names, e.g. "NewServer", "Initialize*". Matching symbols
	// get IsHub set.
	HubPatterns []string
	// Workers is the number of parallel parsers. 0 means GOMAXPROCS.
	Workers int
}

// ErrNoModule is returned when the scan root has no valid go.mod.
var ErrNoModule = errors.New("go.mod not found or has no module directive")

// Scan parses every Go file of the module rooted at opts.Root in parallel and
// indexes its top-level declarations and imports.
//
// Like the go command, it skips vendor and testdata directories, directories
// starting with "." or "_", and nested modules. Build constraints are not
// evaluated except for detecting integration test files, so every file is
// indexed (the conservative choice). Files that fail to parse are indexed on
// a best-effort basis and flagged with FileInfo.ParseError.
func Scan(ctx context.Context, opts ScanOptions) (*RepoIndex, error) {
	if opts.Tag == "" {
		opts.Tag = DefaultIntegrationTag
	}
	if opts.Pattern == "" {
		opts.Pattern = DefaultIntegrationSuffix
	}
	for _, p := range opts.HubPatterns {
		if _, err := path.Match(p, ""); err != nil {
			return nil, fmt.Errorf("invalid hub pattern %q: %w", p, err)
		}
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}

	modPath, err := readModulePath(filepath.Join(opts.Root, "go.mod"))
	if err != nil {
		return nil, err
	}
	files, err := listGoFiles(opts.Root)
	if err != nil {
		return nil, err
	}

	results := make([]*fileResult, len(files))
	jobs := make(chan int)
	var wg sync.WaitGroup
	fset := token.NewFileSet()
	for range min(workers, max(len(files), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = scanFile(fset, opts, files[i])
			}
		}()
	}
	var cancelErr error
feed:
	for i := range files {
		select {
		case jobs <- i:
		case <-ctx.Done():
			cancelErr = ctx.Err()
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if cancelErr != nil {
		return nil, cancelErr
	}

	return buildIndex(modPath, results), nil
}

// readModulePath extracts the module path from a go.mod file.
func readModulePath(gomod string) (string, error) {
	data, err := os.ReadFile(gomod) //nolint:gosec // reading the user's go.mod is the point
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNoModule, err)
	}
	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		rest, ok := strings.CutPrefix(line, "module")
		if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t' && rest[0] != '"') {
			continue
		}
		mod := strings.TrimSpace(rest)
		if strings.HasPrefix(mod, `"`) || strings.HasPrefix(mod, "`") {
			if u, err := strconv.Unquote(mod); err == nil {
				mod = u
			}
		}
		if mod != "" {
			return mod, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNoModule, gomod)
}

// listGoFiles returns the slash-separated paths (relative to root) of every
// Go file the go command would consider part of the module, sorted.
func listGoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p == root {
				return nil
			}
			if name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
				return filepath.SkipDir // nested module
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(name, ".go") ||
			strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing Go files: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

// fileResult is the scan output of one file.
type fileResult struct {
	info    FileInfo
	imports map[string]string // alias -> path; unaliased imports are resolved later
	unnamed []string          // import paths without an explicit name
	symbols []SymbolNode
}

func scanFile(fset *token.FileSet, opts ScanOptions, rel string) *fileResult {
	res := &fileResult{
		info: FileInfo{
			Path:       rel,
			PkgDir:     PkgDirOf(rel),
			IsTestFile: strings.HasSuffix(rel, "_test.go"),
		},
		imports: make(map[string]string),
	}
	src, err := os.ReadFile(filepath.Join(opts.Root, filepath.FromSlash(rel)))
	if err != nil {
		res.info.ParseError = err.Error()
		return res
	}
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		res.info.ParseError = err.Error()
	}
	if f == nil {
		return res
	}
	res.info.PkgName = f.Name.Name
	res.info.IsIntegration = strings.HasSuffix(rel, opts.Pattern) || requiresTag(f, opts.Tag)

	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		switch {
		case imp.Name == nil:
			res.unnamed = append(res.unnamed, p)
		case imp.Name.Name == "_":
		case imp.Name.Name == ".":
			res.imports["."+p] = p
		default:
			res.imports[imp.Name.Name] = p
		}
	}

	sc := symbolCollector{tf: fset.File(f.Pos()), opts: opts, info: &res.info}
	for _, decl := range f.Decls {
		res.symbols = append(res.symbols, sc.decl(decl)...)
	}
	return res
}

// requiresTag reports whether f's //go:build constraint is only satisfied
// when tag is set.
func requiresTag(f *ast.File, tag string) bool {
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			break
		}
		for _, c := range cg.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return false
			}
			// Other tags (GOOS, GOARCH, goX.Y, ...) are treated as satisfied.
			with := expr.Eval(func(string) bool { return true })
			without := expr.Eval(func(t string) bool { return t != tag })
			return with && !without
		}
	}
	return false
}

// symbolCollector turns top-level declarations into SymbolNodes.
type symbolCollector struct {
	tf   *token.File
	opts ScanOptions
	info *FileInfo
}

// line returns the line of p in the collector's file. Positions outside the
// file, which the partial AST of a file with syntax errors may contain, are
// clamped to it; resolving them through the shared FileSet would land in
// whichever file happens to be adjacent.
func (c *symbolCollector) line(p token.Pos) int {
	base, end := c.tf.Base(), c.tf.Base()+c.tf.Size()
	switch {
	case int(p) < base:
		return 1
	case int(p) > end:
		return c.tf.LineCount()
	}
	return c.tf.Line(p)
}

func (c *symbolCollector) newSymbol(kind SymbolKind, name string, start, end token.Pos, refs ...ast.Node) SymbolNode {
	s := SymbolNode{
		Kind:          kind,
		BaseName:      name,
		PkgDir:        c.info.PkgDir,
		FilePath:      c.info.Path,
		StartLine:     c.line(start),
		EndLine:       max(c.line(end), c.line(start)),
		IsIntegration: c.info.IsIntegration,
		Refs:          collectRefs(name, refs...),
	}
	return s
}

func (c *symbolCollector) decl(d ast.Decl) []SymbolNode {
	switch d := d.(type) {
	case *ast.FuncDecl:
		return []SymbolNode{c.funcDecl(d)}
	case *ast.GenDecl:
		return c.genDecl(d)
	}
	return nil
}

func (c *symbolCollector) funcDecl(d *ast.FuncDecl) SymbolNode {
	start := d.Pos()
	if d.Doc != nil {
		start = d.Doc.Pos()
	}
	// Refs include the receiver, signature and body.
	var nodes []ast.Node
	if d.Recv != nil {
		nodes = append(nodes, d.Recv)
	}
	nodes = append(nodes, d.Type)
	if d.Body != nil {
		nodes = append(nodes, d.Body)
	}
	kind := KindFunc
	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = KindMethod
	}
	s := c.newSymbol(kind, d.Name.Name, start, d.End(), nodes...)
	if kind == KindMethod {
		s.ReceiverType = receiverType(d.Recv.List[0].Type)
		if !hasTypeParams(d.Recv.List[0].Type) {
			s.Signature = funcSignature(d.Type)
		}
	}
	s.IsTest = kind == KindFunc && c.info.IsTestFile && isTestFunc(d)
	s.IsHub = c.isHub(s.BaseName)
	return s
}

func (c *symbolCollector) genDecl(d *ast.GenDecl) []SymbolNode {
	if d.Tok == token.IMPORT {
		return nil
	}
	var out []SymbolNode
	for _, spec := range d.Specs {
		// The range of a spec includes its own doc comment, or the GenDecl's
		// doc comment when the declaration is not parenthesized.
		start, end := spec.Pos(), spec.End()
		if !d.Lparen.IsValid() {
			start = d.Pos()
			if d.Doc != nil {
				start = d.Doc.Pos()
			}
		}
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if s.Doc != nil {
				start = s.Doc.Pos()
			}
			kind := KindType
			switch s.Type.(type) {
			case *ast.StructType:
				kind = KindStruct
			case *ast.InterfaceType:
				kind = KindInterface
			}
			nodes := []ast.Node{s.Type}
			if s.TypeParams != nil {
				nodes = append(nodes, s.TypeParams)
			}
			sym := c.newSymbol(kind, s.Name.Name, start, end, nodes...)
			if it, ok := s.Type.(*ast.InterfaceType); ok {
				sym.IfaceMethods, sym.IfaceEmbeds = interfaceMethods(it, s.TypeParams != nil)
			}
			out = append(out, sym)
		case *ast.ValueSpec:
			if s.Doc != nil {
				start = s.Doc.Pos()
			}
			kind := KindVar
			if d.Tok == token.CONST {
				kind = KindConst
				// Constants of a parenthesized group may depend on each other
				// implicitly (iota, repeated expressions): give every member
				// the range of the whole group.
				if d.Lparen.IsValid() {
					start, end = d.Pos(), d.End()
					if d.Doc != nil {
						start = d.Doc.Pos()
					}
				}
			}
			var nodes []ast.Node
			if s.Type != nil {
				nodes = append(nodes, s.Type)
			}
			for _, v := range s.Values {
				nodes = append(nodes, v)
			}
			for _, n := range s.Names {
				out = append(out, c.newSymbol(kind, n.Name, start, end, nodes...))
			}
		}
	}
	return out
}

func (c *symbolCollector) isHub(name string) bool {
	for _, p := range c.opts.HubPatterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// isTestFunc reports whether d has the shape go test runs: TestXxx(t *T)
// where Xxx does not start with a lower-case letter. TestMain is excluded.
func isTestFunc(d *ast.FuncDecl) bool {
	name := d.Name.Name
	if d.Recv != nil || !strings.HasPrefix(name, "Test") || name == "TestMain" {
		return false
	}
	if rest := name[len("Test"):]; rest != "" {
		if r, _ := utf8.DecodeRuneInString(rest); unicode.IsLower(r) {
			return false
		}
	}
	return d.Type.TypeParams == nil && d.Type.Params != nil && len(d.Type.Params.List) == 1 &&
		(d.Type.Results == nil || len(d.Type.Results.List) == 0)
}

// receiverType renders a receiver type expression without type parameters,
// e.g. "*Set" for *Set[T].
func receiverType(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "*" + receiverType(t.X)
	case *ast.ParenExpr:
		return receiverType(t.X)
	case *ast.IndexExpr:
		return receiverType(t.X)
	case *ast.IndexListExpr:
		return receiverType(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// predeclared lists Go's predeclared identifiers, which are never
// references to project symbols.
var predeclared = func() map[string]bool {
	m := make(map[string]bool)
	for _, n := range strings.Fields(`any bool byte comparable complex64 complex128 error float32 float64
		int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr
		true false iota nil
		append cap clear close complex copy delete imag len make max min new panic print println real recover`) {
		m[n] = true
	}
	m["_"] = true
	return m
}()

// collectRefs returns the sorted, deduplicated identifiers used in nodes,
// excluding predeclared identifiers and self.
//
// Package qualifiers are kept: for `repo.NewX` both "repo" and "NewX" are
// reported, so that the analyzer can tell package-qualified references apart
// using the file's ImportMap.
func collectRefs(self string, nodes ...ast.Node) []string {
	seen := make(map[string]bool)
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && !predeclared[id.Name] && id.Name != self {
				seen[id.Name] = true
			}
			return true
		})
	}
	refs := make([]string, 0, len(seen))
	for r := range seen {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return refs
}

// buildIndex merges per-file results: it resolves unaliased imports to
// package names, assigns symbol IDs and sorts everything.
func buildIndex(modPath string, results []*fileResult) *RepoIndex {
	idx := &RepoIndex{
		ModulePath:  modPath,
		Files:       make([]FileInfo, 0, len(results)),
		FileImports: make(map[string]map[string]string, len(results)),
	}

	// Actual package names of in-module directories (non-test packages win).
	dirNames := make(map[string]string)
	for _, r := range results {
		if r.info.PkgName == "" || strings.HasSuffix(r.info.PkgName, "_test") {
			continue
		}
		if _, ok := dirNames[r.info.PkgDir]; !ok || r.info.PkgName != "main" {
			dirNames[r.info.PkgDir] = r.info.PkgName
		}
	}

	for _, r := range results {
		for _, p := range r.unnamed {
			name := ""
			if dir := PkgDirForImport(modPath, p); dir != "" {
				name = dirNames[dir]
			}
			if name == "" {
				name = GuessPackageName(p)
			}
			r.imports[name] = p
		}
		idx.Files = append(idx.Files, r.info)
		idx.FileImports[r.info.Path] = r.imports

		self := modPath
		if r.info.PkgDir != "." {
			self += "/" + strings.TrimPrefix(r.info.PkgDir, "./")
		}
		for i := range r.symbols {
			s := &r.symbols[i]
			s.ID = symbolID(r.info, s)
			s.Signature = qualifySignature(s.Signature, self, r.imports)
			for name, sig := range s.IfaceMethods {
				s.IfaceMethods[name] = qualifySignature(sig, self, r.imports)
			}
			idx.Symbols = append(idx.Symbols, *s)
		}
	}

	sort.Slice(idx.Files, func(i, j int) bool { return idx.Files[i].Path < idx.Files[j].Path })
	sort.SliceStable(idx.Symbols, func(i, j int) bool {
		a, b := idx.Symbols[i], idx.Symbols[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.FilePath != b.FilePath {
			return a.FilePath < b.FilePath
		}
		return a.StartLine < b.StartLine
	})
	// Make IDs unique: init functions, blank identifiers and declarations
	// duplicated across build-constrained files get a "@file:line" suffix.
	for i := 0; i < len(idx.Symbols); {
		j := i + 1
		for j < len(idx.Symbols) && idx.Symbols[j].ID == idx.Symbols[i].ID {
			j++
		}
		if j-i > 1 {
			for k := i; k < j; k++ {
				s := &idx.Symbols[k]
				s.ID += "@" + s.FilePath + ":" + strconv.Itoa(s.StartLine)
			}
		}
		i = j
	}
	return idx
}

// symbolID renders the ID of s, e.g. "./svc.(*impl).Create" or
// "./svc_test.TestCreate" for an external test package.
func symbolID(f FileInfo, s *SymbolNode) string {
	pkg := f.PkgDir
	if strings.HasSuffix(f.PkgName, "_test") {
		pkg += "_test"
	}
	switch {
	case s.Kind != KindMethod:
		return pkg + "." + s.BaseName
	case strings.HasPrefix(s.ReceiverType, "*"):
		return pkg + ".(" + s.ReceiverType + ")." + s.BaseName
	default:
		return pkg + "." + s.ReceiverType + "." + s.BaseName
	}
}

// PkgDirForImport returns the "./dir" of an import path inside module
// modPath, or "" when the import is outside the module.
func PkgDirForImport(modPath, importPath string) string {
	switch {
	case modPath == "":
		return ""
	case importPath == modPath:
		return "."
	case strings.HasPrefix(importPath, modPath+"/"):
		return "./" + strings.TrimPrefix(importPath, modPath+"/")
	}
	return ""
}

// GuessPackageName guesses the package name of an import path outside the
// module from its last element, ignoring major version suffixes ("/v2"),
// gopkg.in-style suffixes (".v3") and "go-" prefixes / "-go" suffixes.
func GuessPackageName(importPath string) string {
	elems := strings.Split(importPath, "/")
	name := elems[len(elems)-1]
	if len(elems) > 1 && isMajorVersion(name) {
		name = elems[len(elems)-2]
	}
	if i := strings.Index(name, ".v"); i > 0 && isMajorVersion(name[i+1:]) {
		name = name[:i]
	}
	name = strings.TrimPrefix(name, "go-")
	name = strings.TrimSuffix(name, "-go")
	name = strings.TrimSuffix(name, ".go")
	return strings.NewReplacer("-", "_", ".", "_").Replace(name)
}

func isMajorVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	_, err := strconv.Atoi(s[1:])
	return err == nil
}

// Signature normalization. While scanning, named types are rendered with a
// placeholder qualifier: "{.}Name" for the file's own package and
// "{alias}.Name" for imported ones. buildIndex replaces the placeholders with
// import paths once unaliased imports are resolved to package names.

// errUnknownType aborts rendering of types whose identity cannot be
// determined without type checking.
var errUnknownType = errors.New("unknown type")

// funcSignature renders "(<params>)(<results>)" or "" when unknown.
func funcSignature(ft *ast.FuncType) string {
	s, err := renderFunc(ft)
	if err != nil {
		return ""
	}
	return s
}

func renderFunc(ft *ast.FuncType) (string, error) {
	if ft.TypeParams != nil {
		return "", errUnknownType
	}
	params, err := renderFields(ft.Params)
	if err != nil {
		return "", err
	}
	results, err := renderFields(ft.Results)
	if err != nil {
		return "", err
	}
	return "(" + params + ")(" + results + ")", nil
}

func renderFields(fl *ast.FieldList) (string, error) {
	if fl == nil {
		return "", nil
	}
	var parts []string
	for _, f := range fl.List {
		t, err := renderType(f.Type)
		if err != nil {
			return "", err
		}
		for range max(len(f.Names), 1) {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, ","), nil
}

func renderType(e ast.Expr) (string, error) {
	switch t := e.(type) {
	case *ast.Ident:
		if predeclared[t.Name] {
			if t.Name == "any" {
				return "interface{}", nil
			}
			return t.Name, nil
		}
		return "{.}" + t.Name, nil
	case *ast.SelectorExpr:
		x, ok := t.X.(*ast.Ident)
		if !ok {
			return "", errUnknownType
		}
		return "{" + x.Name + "}." + t.Sel.Name, nil
	case *ast.ParenExpr:
		return renderType(t.X)
	case *ast.StarExpr:
		return prefixed("*", t.X)
	case *ast.Ellipsis:
		return prefixed("...", t.Elt)
	case *ast.ArrayType:
		if t.Len == nil {
			return prefixed("[]", t.Elt)
		}
		if lit, ok := t.Len.(*ast.BasicLit); ok {
			return prefixed("["+lit.Value+"]", t.Elt)
		}
		return "", errUnknownType
	case *ast.MapType:
		k, err := renderType(t.Key)
		if err != nil {
			return "", err
		}
		return prefixed("map["+k+"]", t.Value)
	case *ast.ChanType:
		switch t.Dir {
		case ast.SEND:
			return prefixed("chan<- ", t.Value)
		case ast.RECV:
			return prefixed("<-chan ", t.Value)
		default:
			return prefixed("chan ", t.Value)
		}
	case *ast.FuncType:
		s, err := renderFunc(t)
		return "func" + s, err
	case *ast.InterfaceType:
		if t.Methods == nil || len(t.Methods.List) == 0 {
			return "interface{}", nil
		}
	case *ast.StructType:
		if t.Fields == nil || len(t.Fields.List) == 0 {
			return "struct{}", nil
		}
	}
	return "", errUnknownType
}

func prefixed(prefix string, e ast.Expr) (string, error) {
	s, err := renderType(e)
	return prefix + s, err
}

// interfaceMethods returns the declared methods of an interface with their
// signatures, and whether it embeds other types. Methods of generic
// interfaces get unknown ("") signatures.
func interfaceMethods(it *ast.InterfaceType, generic bool) (map[string]string, bool) {
	methods := make(map[string]string)
	embeds := false
	if it.Methods == nil {
		return methods, false
	}
	for _, f := range it.Methods.List {
		ft, ok := f.Type.(*ast.FuncType)
		if !ok || len(f.Names) == 0 {
			embeds = true
			continue
		}
		sig := ""
		if !generic {
			sig = funcSignature(ft)
		}
		for _, n := range f.Names {
			methods[n.Name] = sig
		}
	}
	return methods, embeds
}

// hasTypeParams reports whether a receiver type expression is generic.
func hasTypeParams(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.StarExpr:
		return hasTypeParams(t.X)
	case *ast.ParenExpr:
		return hasTypeParams(t.X)
	case *ast.IndexExpr, *ast.IndexListExpr:
		return true
	}
	return false
}

var placeholder = regexp.MustCompile(`\{([^}]*)\}`)

// qualifySignature replaces placeholders with import paths. It returns ""
// (unknown) when a qualifier cannot be resolved.
func qualifySignature(sig, self string, imports map[string]string) string {
	if sig == "" {
		return ""
	}
	for alias := range imports {
		if strings.HasPrefix(alias, ".") {
			return "" // dot-imported names are indistinguishable from local ones
		}
	}
	unknown := false
	out := placeholder.ReplaceAllStringFunc(sig, func(m string) string {
		alias := m[1 : len(m)-1]
		if alias == "." {
			return self + "."
		}
		if p, ok := imports[alias]; ok {
			return p
		}
		unknown = true
		return m
	})
	if unknown {
		return ""
	}
	return out
}
