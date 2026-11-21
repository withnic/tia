package tia

import (
	"path"
	"sort"
	"strings"
)

// SeedReason explains why a symbol became a seed.
type SeedReason string

// Seed reasons.
const (
	// ReasonChangedLines: a changed line falls inside the declaration.
	ReasonChangedLines SeedReason = "changed-lines"
	// ReasonTopLevel: a changed line is outside every declaration (package
	// clause, imports, removed declarations), so every declaration of the
	// file is a seed.
	ReasonTopLevel SeedReason = "top-level-change"
	// ReasonWholeFile: the file is new, untracked, binary or otherwise
	// changed as a whole.
	ReasonWholeFile SeedReason = "whole-file"
	// ReasonDeletedFile: a Go file of the same package was deleted.
	ReasonDeletedFile SeedReason = "deleted-file"
	// ReasonUnparsable: the changed file has syntax errors, so its package
	// is seeded entirely.
	ReasonUnparsable SeedReason = "unparsable-file"
	// ReasonNonGoFile: a non-Go file (e.g. .sql) in the package directory
	// changed.
	ReasonNonGoFile SeedReason = "non-go-file"
	// ReasonTestdata: a file under the package's testdata directory changed;
	// only the package's tests are seeded.
	ReasonTestdata SeedReason = "testdata"
)

// Seed is a symbol the reverse traversal starts from.
type Seed struct {
	Symbol SymbolNode
	// Reason is the first reason (in diff order) the symbol was seeded for.
	Reason SeedReason
	// Cause is the changed file that triggered the seed.
	Cause string
}

// SeedSet is the result of FindSeeds.
type SeedSet struct {
	// Seeds lists the seeded symbols, sorted by symbol ID.
	Seeds []Seed
	// NonGoFiles lists every changed file that is not a .go file, sorted.
	// The analyzer can apply fallbacks for them (e.g. go.mod, migrations).
	NonGoFiles []string
}

// docExts are documentation files whose changes never affect tests.
var docExts = map[string]bool{".md": true, ".markdown": true, ".rst": true, ".adoc": true, ".txt": true}

// moduleFiles affect the whole module rather than the package in their
// directory; they are only reported in SeedSet.NonGoFiles.
var moduleFiles = map[string]bool{"go.mod": true, "go.sum": true, "go.work": true, "go.work.sum": true}

// seedsDirectory reports whether a change to the non-Go file p should seed
// the package in the same directory.
func seedsDirectory(p string) bool {
	base := path.Base(p)
	switch {
	case moduleFiles[base], strings.HasPrefix(base, "."), strings.HasPrefix(base, "LICENSE"):
		return false
	}
	return !docExts[strings.ToLower(path.Ext(base))]
}

// FindSeeds maps changed line ranges to the top-level declarations they
// touch. It errs on the side of seeding too much (docs/04_guidelines.md §3):
//
//   - a change inside a declaration (including its doc comment) seeds it;
//   - a change outside every declaration, a whole-file change or a change to
//     a file that failed to parse seeds every declaration of the file
//     (of the package, for unparsable files);
//   - a deleted Go file seeds every declaration of its package directory;
//   - a changed non-Go file seeds every declaration of the package in the
//     same directory, except documentation (.md, .txt, ...), dotfiles,
//     LICENSE and module files (go.mod, go.sum, go.work), which are only
//     reported in NonGoFiles;
//   - a changed file under a testdata directory seeds the tests of the
//     package owning that testdata directory.
func FindSeeds(idx *RepoIndex, ranges []DiffRange) *SeedSet {
	byFile := make(map[string][]*SymbolNode)
	byDir := make(map[string][]*SymbolNode)
	for i := range idx.Symbols {
		s := &idx.Symbols[i]
		byFile[s.FilePath] = append(byFile[s.FilePath], s)
		byDir[s.PkgDir] = append(byDir[s.PkgDir], s)
	}
	files := make(map[string]*FileInfo, len(idx.Files))
	for i := range idx.Files {
		files[idx.Files[i].Path] = &idx.Files[i]
	}

	seeds := make(map[string]Seed)
	add := func(s *SymbolNode, reason SeedReason, cause string) {
		if _, ok := seeds[s.ID]; !ok {
			seeds[s.ID] = Seed{Symbol: *s, Reason: reason, Cause: cause}
		}
	}
	addAll := func(syms []*SymbolNode, reason SeedReason, cause string) {
		for _, s := range syms {
			add(s, reason, cause)
		}
	}
	nonGo := make(map[string]bool)

	for _, r := range ranges {
		p := r.FilePath
		if dir, ok := testdataOwner(p); ok {
			for _, s := range byDir[dir] {
				if s.IsTest {
					add(s, ReasonTestdata, p)
				}
			}
			if !strings.HasSuffix(p, ".go") {
				nonGo[p] = true
			}
			continue
		}
		if !strings.HasSuffix(p, ".go") {
			nonGo[p] = true
			if seedsDirectory(p) {
				addAll(byDir[PkgDirOf(p)], ReasonNonGoFile, p)
			}
			continue
		}
		if r.Deleted {
			addAll(byDir[PkgDirOf(p)], ReasonDeletedFile, p)
			continue
		}
		f, scanned := files[p]
		switch {
		case !scanned:
			// Skipped by the scanner (vendor, nested module, ...).
		case f.ParseError != "":
			addAll(byDir[f.PkgDir], ReasonUnparsable, p)
		case r.WholeFile():
			addAll(byFile[p], ReasonWholeFile, p)
		default:
			hit, covered := overlapping(byFile[p], r.StartLine, r.EndLine)
			if !covered {
				addAll(byFile[p], ReasonTopLevel, p)
			}
			addAll(hit, ReasonChangedLines, p)
		}
	}

	out := &SeedSet{Seeds: make([]Seed, 0, len(seeds))}
	for id := range seeds {
		out.Seeds = append(out.Seeds, seeds[id])
	}
	sort.Slice(out.Seeds, func(i, j int) bool { return out.Seeds[i].Symbol.ID < out.Seeds[j].Symbol.ID })
	for p := range nonGo {
		out.NonGoFiles = append(out.NonGoFiles, p)
	}
	sort.Strings(out.NonGoFiles)
	return out
}

// overlapping returns the symbols intersecting lines [start, end] and whether
// every line of the range is covered by some symbol.
func overlapping(syms []*SymbolNode, start, end int) (hit []*SymbolNode, covered bool) {
	type span struct{ s, e int }
	var spans []span
	for _, s := range syms {
		if s.StartLine <= end && s.EndLine >= start {
			hit = append(hit, s)
			spans = append(spans, span{max(s.StartLine, start), min(s.EndLine, end)})
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].s < spans[j].s })
	next := start // first line not yet known to be covered
	for _, sp := range spans {
		if sp.s > next {
			return hit, false
		}
		next = max(next, sp.e+1)
	}
	return hit, next > end
}

// testdataOwner reports whether p lies under a testdata directory and returns
// the package directory owning it.
func testdataOwner(p string) (string, bool) {
	elems := strings.Split(p, "/")
	for i, e := range elems[:len(elems)-1] {
		if e == "testdata" {
			if i == 0 {
				return ".", true
			}
			return "./" + strings.Join(elems[:i], "/"), true
		}
	}
	return "", false
}
