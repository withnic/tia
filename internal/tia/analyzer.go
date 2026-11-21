package tia

import (
	"path"
	"sort"
	"strings"
)

// SelectReason classifies why an integration test was selected.
type SelectReason string

// Selection reasons.
const (
	// SelectChanged: the test function itself (or its file) changed.
	SelectChanged SelectReason = "changed"
	// SelectReference: the test reaches a seed through references.
	SelectReference SelectReason = "reference"
	// SelectFallback: a change that cannot be analyzed statically (go.mod,
	// migrations, ...) selects every integration test.
	SelectFallback SelectReason = "fallback"
)

// SelectedTest is one integration test chosen for execution.
type SelectedTest struct {
	Symbol SymbolNode
	Reason SelectReason
	// Path explains the selection: the reference chain from the test back
	// to the seed (test first), the injector name, or the fallback file.
	Path []string
}

// Selection is the result of Analyze.
type Selection struct {
	// Tests lists the selected integration tests, sorted by symbol ID.
	Tests []SelectedTest
	// Affected lists every symbol ID reached by the reverse traversal,
	// sorted.
	Affected []string
	// StoppedAtHubs lists the hub symbols where the traversal stopped.
	StoppedAtHubs []string
	// Fallback is the changed file that triggered running every integration
	// test, or empty.
	Fallback string
}

// Analyze runs the reverse traversal from seeds and selects the affected
// integration tests.
//
// Edges (referrer -> symbol) are connected conservatively but import-aware,
// so that same-named symbols of unrelated packages do not leak in:
//
//   - same package directory: the base name is referenced;
//   - other package, non-method: the referrer's file imports the symbol's
//     package and uses its qualifier (or dot-imports it);
//   - other package, method: the referrer's file imports the method's
//     package, an in-module package that imports it (values returned by
//     constructors or injectors), or a package declaring an interface
//     method with the same name and signature (calls through interfaces
//     cannot be resolved statically; unknown signatures match).
//
// Traversal does not continue past hub symbols (unless they are seeds).
func Analyze(idx *RepoIndex, seeds *SeedSet) *Selection {
	g := newRefGraph(idx)
	sel := &Selection{}

	if f := fallbackTrigger(g, seeds.NonGoFiles); f != "" {
		sel.Fallback = f
		for i := range idx.Symbols {
			s := &idx.Symbols[i]
			if s.IsTest && s.IsIntegration {
				sel.Tests = append(sel.Tests, SelectedTest{Symbol: *s, Reason: SelectFallback, Path: []string{f}})
			}
		}
		return sel
	}

	bfs := reverseBFS(idx, g, seeds)
	sel.StoppedAtHubs = bfs.hubs

	for i := range bfs.parent {
		s := &idx.Symbols[i]
		sel.Affected = append(sel.Affected, s.ID)
		if s.IsTest && s.IsIntegration {
			reason := SelectReference
			if bfs.isSeed[i] {
				reason = SelectChanged
			}
			sel.Tests = append(sel.Tests, SelectedTest{Symbol: *s, Reason: reason, Path: bfs.chain(idx, i)})
		}
	}

	sort.Slice(sel.Tests, func(i, j int) bool { return sel.Tests[i].Symbol.ID < sel.Tests[j].Symbol.ID })
	sort.Strings(sel.Affected)
	sort.Strings(sel.StoppedAtHubs)
	return sel
}

// bfsResult holds the state produced by a reverse BFS traversal.
type bfsResult struct {
	parent map[int]int
	isSeed map[int]bool
	hubs   []string
}

// chain returns the reference path from symbol i back to its seed.
func (b *bfsResult) chain(idx *RepoIndex, i int) []string {
	var p []string
	for ; i >= 0; i = b.parent[i] {
		p = append(p, idx.Symbols[i].ID)
	}
	return p
}

// reverseBFS walks references in reverse from seeds, stopping at hub symbols.
func reverseBFS(idx *RepoIndex, g *refGraph, seeds *SeedSet) *bfsResult {
	b := &bfsResult{
		parent: make(map[int]int),
		isSeed: make(map[int]bool),
	}
	var queue []int
	for k := range seeds.Seeds {
		for _, i := range g.lookup(seeds.Seeds[k].Symbol.ID) {
			if _, ok := b.parent[i]; !ok {
				b.parent[i] = -1
				b.isSeed[i] = true
				queue = append(queue, i)
			}
		}
	}
	for len(queue) > 0 {
		si := queue[0]
		queue = queue[1:]
		s := &idx.Symbols[si]
		if s.IsHub && !b.isSeed[si] {
			b.hubs = append(b.hubs, s.ID)
			continue
		}
		for _, xi := range g.referrers[s.BaseName] {
			if _, seen := b.parent[xi]; seen || !g.refersTo(&idx.Symbols[xi], s) {
				continue
			}
			b.parent[xi] = si
			queue = append(queue, xi)
		}
	}
	return b
}

// isMigration reports whether p is an SQL or migration file.
func isMigration(p string) bool {
	return strings.EqualFold(path.Ext(p), ".sql") || strings.Contains(strings.ToLower(p), "migration")
}

// fallbackTrigger returns the first changed non-Go file that requires
// running every integration test: module files, or SQL / migration files
// that are not next to a Go package (those seed that package instead).
func fallbackTrigger(g *refGraph, nonGo []string) string {
	for _, p := range nonGo {
		if _, ok := testdataOwner(p); ok {
			continue
		}
		base := path.Base(p)
		if moduleFiles[base] {
			return p
		}
		if isMigration(p) && !g.hasDir[PkgDirOf(p)] {
			return p
		}
	}
	return ""
}

// refGraph holds the lookup tables of the reverse traversal.
type refGraph struct {
	idx *RepoIndex
	// byBaseID maps an ID without its "@file:line" suffix to symbols.
	byBaseID map[string][]int
	// referrers maps a name to the symbols whose Refs contain it.
	referrers map[string][]int
	// externalTest marks files of external test packages (package x_test).
	externalTest map[string]bool
	// hasDir marks package directories that contain symbols.
	hasDir map[string]bool
	// ifaceMethods maps a method name to the interfaces declaring it.
	ifaceMethods map[string][]ifaceMethod
	// dirImports maps a package directory to the import paths used by its
	// non-test files.
	dirImports map[string]map[string]bool
}

func newRefGraph(idx *RepoIndex) *refGraph {
	g := &refGraph{
		idx:          idx,
		byBaseID:     make(map[string][]int, len(idx.Symbols)),
		referrers:    make(map[string][]int),
		externalTest: make(map[string]bool),
		hasDir:       make(map[string]bool),
		ifaceMethods: make(map[string][]ifaceMethod),
		dirImports:   make(map[string]map[string]bool),
	}
	for _, f := range idx.Files {
		if strings.HasSuffix(f.PkgName, "_test") {
			g.externalTest[f.Path] = true
		}
		if f.IsTestFile {
			continue
		}
		if g.dirImports[f.PkgDir] == nil {
			g.dirImports[f.PkgDir] = make(map[string]bool)
		}
		for _, p := range idx.FileImports[f.Path] {
			g.dirImports[f.PkgDir][p] = true
		}
	}
	for i := range idx.Symbols {
		s := &idx.Symbols[i]
		g.byBaseID[baseID(s.ID)] = append(g.byBaseID[baseID(s.ID)], i)
		g.hasDir[s.PkgDir] = true
		for _, r := range s.Refs {
			g.referrers[r] = append(g.referrers[r], i)
		}
		if s.Kind == KindInterface {
			for name, sig := range s.IfaceMethods {
				g.ifaceMethods[name] = append(g.ifaceMethods[name], ifaceMethod{dir: s.PkgDir, sig: sig})
			}
			if s.IfaceEmbeds {
				// The method set is partly unknown: any name mentioned may
				// be a method, with any signature.
				for _, r := range s.Refs {
					g.ifaceMethods[r] = append(g.ifaceMethods[r], ifaceMethod{dir: s.PkgDir})
				}
			}
		}
	}
	return g
}

// ifaceMethod is a method declared by an interface in dir. An empty sig
// matches any signature.
type ifaceMethod struct{ dir, sig string }

// baseID strips the "@file:line" disambiguation suffix from a symbol ID.
func baseID(id string) string {
	if i := strings.LastIndex(id, "@"); i >= 0 {
		return id[:i]
	}
	return id
}

func (g *refGraph) lookup(id string) []int { return g.byBaseID[baseID(id)] }

// importPath returns the import path of a package directory.
func (g *refGraph) importPath(dir string) string {
	if dir == "." {
		return g.idx.ModulePath
	}
	return g.idx.ModulePath + "/" + strings.TrimPrefix(dir, "./")
}

// refersTo reports whether referrer x (which mentions s.BaseName) may refer
// to s.
func (g *refGraph) refersTo(x, s *SymbolNode) bool {
	if x.PkgDir == s.PkgDir {
		return true
	}
	if g.externalTest[s.FilePath] {
		return false // nothing outside the directory can import x_test
	}
	imports := g.idx.FileImports[x.FilePath]
	target := g.importPath(s.PkgDir)
	if s.Kind == KindMethod {
		if importsPath(imports, target) {
			return true
		}
		// One hop: the value may come from an in-module package that
		// itself imports the method's package (e.g. a constructor or an
		// injector returning it).
		for _, p := range imports {
			if dir := PkgDirForImport(g.idx.ModulePath, p); dir != "" && g.dirImports[dir][target] {
				return true
			}
		}
		for _, m := range g.ifaceMethods[s.BaseName] {
			if m.sig != "" && s.Signature != "" && m.sig != s.Signature {
				continue // s cannot implement this interface method
			}
			if m.dir == x.PkgDir || importsPath(imports, g.importPath(m.dir)) {
				return true
			}
		}
		return false
	}
	for name, p := range imports {
		if p == target && (strings.HasPrefix(name, ".") || hasRef(x, name)) {
			return true
		}
	}
	return false
}

func importsPath(imports map[string]string, target string) bool {
	for _, p := range imports {
		if p == target {
			return true
		}
	}
	return false
}

func hasRef(s *SymbolNode, name string) bool {
	i := sort.SearchStrings(s.Refs, name)
	return i < len(s.Refs) && s.Refs[i] == name
}
