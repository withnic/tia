// Package tia implements the Test Impact Analysis engine: it maps a git diff
// to the integration tests that may be affected by it.
//
// The package intentionally depends on the standard library only
// (docs/04_guidelines.md §1): parsing is done with go/parser without type
// checking so that a whole repository can be indexed in well under a second.
package tia

import (
	"fmt"
	"strings"
)

// SymbolKind classifies a top-level declaration.
type SymbolKind int

// Symbol kinds. KindType covers type declarations that are neither structs
// nor interfaces (e.g. `type ID string`, aliases); they are tracked too so
// that changes to them are never missed.
const (
	KindFunc SymbolKind = iota + 1
	KindMethod
	KindVar
	KindConst
	KindStruct
	KindInterface
	KindType
)

var kindNames = map[SymbolKind]string{
	KindFunc:      "Func",
	KindMethod:    "Method",
	KindVar:       "Var",
	KindConst:     "Const",
	KindStruct:    "Struct",
	KindInterface: "Interface",
	KindType:      "Type",
}

// String returns the kind name, e.g. "Method".
func (k SymbolKind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("SymbolKind(%d)", int(k))
}

// MarshalText implements encoding.TextMarshaler.
func (k SymbolKind) MarshalText() ([]byte, error) { return []byte(k.String()), nil }

// SymbolNode is a top-level declaration found by the scanner.
type SymbolNode struct {
	// ID uniquely identifies the symbol, e.g.
	// "./internal/service.(*userServiceImpl).Create". Declarations of an
	// external test package (package foo_test) use the directory followed by
	// "_test", e.g. "./internal/service_test.TestCreate". Symbols of the root
	// package are prefixed with ".", e.g. "..main".
	ID string
	// Kind is the declaration kind.
	Kind SymbolKind
	// BaseName is the declared identifier, e.g. Create, MaxRetry.
	BaseName string
	// ReceiverType is the receiver of a method, e.g. "*userServiceImpl".
	// Type parameters are dropped ("*Set" for `func (s *Set[T]) Add()`).
	ReceiverType string
	// PkgDir is the package directory relative to the scan root, e.g.
	// "./internal/service" ("." for the root).
	PkgDir string
	// FilePath is the slash-separated file path relative to the scan root.
	FilePath string
	// StartLine is the first line of the declaration, including its doc
	// comment.
	StartLine int
	// EndLine is the last line of the declaration.
	EndLine int
	// IsTest reports whether the symbol is a TestXxx function in a _test.go
	// file.
	IsTest bool
	// IsIntegration reports whether the declaring file is an integration test
	// file (build tag or file name suffix).
	IsIntegration bool
	// IsHub reports whether the symbol matches a hub pattern; reverse
	// traversal stops at hubs.
	IsHub bool
	// Refs lists, sorted and deduplicated, the identifiers referenced inside
	// the declaration (base names only; package qualifiers are resolved via
	// the file's ImportMap).
	Refs []string
	// Signature is the normalized signature of a method, e.g.
	// "(context.Context,string)(error)" with every named type qualified by
	// its import path. Empty when it cannot be determined statically
	// (generic receivers, anonymous structs, ...), which matches any
	// signature.
	Signature string
	// IfaceMethods maps the methods declared by an interface to their
	// normalized signatures (interfaces only).
	IfaceMethods map[string]string
	// IfaceEmbeds reports that an interface embeds other interfaces or type
	// constraints, so its method set is not fully known.
	IfaceEmbeds bool
}

// DiffRange is a range of changed lines in one file, taken from
// `git diff --unified=0`. Line numbers refer to the working tree version.
type DiffRange struct {
	// FilePath is the slash-separated path relative to the scan root.
	FilePath string
	// StartLine is the first changed line. 0 means the whole file changed
	// (e.g. untracked or binary files); EndLine is then 0 as well.
	StartLine int
	// EndLine is the last changed line (inclusive).
	EndLine int
	// Deleted reports that the file was removed.
	Deleted bool
}

// WholeFile reports whether r covers the whole file.
func (r DiffRange) WholeFile() bool { return r.StartLine == 0 }

// ImportMap maps the identifiers a file uses for its imports to the full
// import paths.
type ImportMap struct {
	// FilePath is the slash-separated file path relative to the scan root.
	FilePath string
	// Imports maps the local package name (alias or package name) to the
	// import path. Dot imports are keyed "." + import path; blank imports are
	// omitted.
	Imports map[string]string
}

// FileInfo describes one scanned Go file.
type FileInfo struct {
	// Path is the slash-separated file path relative to the scan root.
	Path string
	// PkgDir is the package directory, e.g. "./internal/service".
	PkgDir string
	// PkgName is the name in the package clause.
	PkgName string
	// IsTestFile reports whether the file name ends in _test.go.
	IsTestFile bool
	// IsIntegration reports whether the file is an integration test file.
	IsIntegration bool
	// ParseError is non-empty when the file could not be parsed completely;
	// its symbols are then best-effort and callers should be conservative.
	ParseError string
}

// RepoIndex is the scan result for a whole module.
type RepoIndex struct {
	// ModulePath is the module path declared in go.mod.
	ModulePath string
	// Symbols lists every top-level declaration, sorted by ID.
	Symbols []SymbolNode
	// Files lists every scanned Go file, sorted by Path.
	Files []FileInfo
	// FileImports is the ImportMap index keyed by file path.
	FileImports map[string]map[string]string
}

// ImportMap returns the ImportMap of the file at path.
func (idx *RepoIndex) ImportMap(path string) ImportMap {
	return ImportMap{FilePath: path, Imports: idx.FileImports[path]}
}

// PkgDirOf returns the package directory of a slash-separated path relative
// to the scan root, e.g. "internal/service/user.go" -> "./internal/service".
func PkgDirOf(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return "."
	}
	return "./" + path[:i]
}
