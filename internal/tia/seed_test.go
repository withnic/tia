package tia

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func seedIDs(s *SeedSet) []string {
	ids := make([]string, len(s.Seeds))
	for i, sd := range s.Seeds {
		ids[i] = sd.Symbol.ID
	}
	return ids
}

// fileSymbolIDs returns the sorted IDs of every symbol declared in file
// (or, for a directory "./x", in the package directory).
func fileSymbolIDs(idx *RepoIndex, match func(SymbolNode) bool) []string {
	var ids []string
	for _, s := range idx.Symbols {
		if match(s) {
			ids = append(ids, s.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func TestFindSeeds(t *testing.T) {
	idx := scanFixture(t)
	const user = "internal/service/user.go"
	createLine := lineOf(t, user, "return s.repo.Create")
	inFile := func(f string) func(SymbolNode) bool { return func(s SymbolNode) bool { return s.FilePath == f } }
	inDir := func(d string) func(SymbolNode) bool { return func(s SymbolNode) bool { return s.PkgDir == d } }
	statusGroup := []string{"./internal/service.StatusActive", "./internal/service.StatusInactive"}

	tests := []struct {
		name       string
		ranges     []DiffRange
		want       []string
		wantReason SeedReason
		wantNonGo  []string
	}{
		{
			name:       "change inside a method body",
			ranges:     []DiffRange{{FilePath: user, StartLine: createLine, EndLine: createLine}},
			want:       []string{"./internal/service.(*userServiceImpl).Create"},
			wantReason: ReasonChangedLines,
		},
		{
			name:       "doc comment belongs to the declaration",
			ranges:     []DiffRange{{FilePath: user, StartLine: lineOf(t, user, "// NewUserService"), EndLine: lineOf(t, user, "// NewUserService")}},
			want:       []string{"./internal/service.NewUserService"},
			wantReason: ReasonChangedLines,
		},
		{
			// The blank line between the two methods is outside every
			// declaration: conservatively seed the whole file.
			name:       "range spanning two declarations and the gap between them",
			ranges:     []DiffRange{{FilePath: user, StartLine: lineOf(t, user, "func (s *Set[T])"), EndLine: lineOf(t, user, "func (s Set[T])")}},
			want:       fileSymbolIDs(idx, inFile(user)),
			wantReason: ReasonTopLevel,
		},
		{
			name:       "one const of an iota group seeds the whole group",
			ranges:     []DiffRange{{FilePath: user, StartLine: lineOf(t, user, "StatusActive = iota"), EndLine: lineOf(t, user, "StatusActive = iota")}},
			want:       statusGroup,
			wantReason: ReasonChangedLines,
		},
		{
			name:       "import change seeds every declaration of the file",
			ranges:     []DiffRange{{FilePath: user, StartLine: lineOf(t, user, `"context"`), EndLine: lineOf(t, user, `"context"`)}},
			want:       fileSymbolIDs(idx, inFile(user)),
			wantReason: ReasonTopLevel,
		},
		{
			name:       "deletion between declarations is a top-level change",
			ranges:     []DiffRange{{FilePath: user, StartLine: lineOf(t, user, "type ID string") - 1, EndLine: lineOf(t, user, "type ID string") - 1}},
			want:       fileSymbolIDs(idx, inFile(user)),
			wantReason: ReasonTopLevel,
		},
		{
			name:       "whole-file change",
			ranges:     []DiffRange{{FilePath: "internal/server/server.go"}},
			want:       []string{"./internal/server.InitializeRoutes", "./internal/server.NewServer", "./internal/server.Server"},
			wantReason: ReasonWholeFile,
		},
		{
			name:       "changed test function is itself a seed",
			ranges:     []DiffRange{{FilePath: "internal/service/user_integration_test.go", StartLine: lineOf(t, "internal/service/user_integration_test.go", "func TestCreateUser"), EndLine: lineOf(t, "internal/service/user_integration_test.go", "func TestCreateUser")}},
			want:       []string{"./internal/service_test.TestCreateUser"},
			wantReason: ReasonChangedLines,
		},
		{
			name:       "deleted Go file seeds its package directory",
			ranges:     []DiffRange{{FilePath: "internal/repo/removed.go", Deleted: true}},
			want:       fileSymbolIDs(idx, inDir("./internal/repo")),
			wantReason: ReasonDeletedFile,
		},
		{
			name:       "unparsable file seeds its package directory",
			ranges:     []DiffRange{{FilePath: "internal/broken/broken.go", StartLine: 3, EndLine: 3}},
			want:       fileSymbolIDs(idx, inDir("./internal/broken")),
			wantReason: ReasonUnparsable,
		},
		{
			name:       ".sql change seeds the Go package in the same directory",
			ranges:     []DiffRange{{FilePath: "internal/repo/schema.sql", StartLine: 1, EndLine: 1}},
			want:       fileSymbolIDs(idx, inDir("./internal/repo")),
			wantReason: ReasonNonGoFile,
			wantNonGo:  []string{"internal/repo/schema.sql"},
		},
		{
			name:      "documentation and go.mod changes seed nothing but are reported",
			ranges:    []DiffRange{{FilePath: "internal/repo/README.md", StartLine: 1, EndLine: 1}, {FilePath: "go.sum"}, {FilePath: ".golangci.yml"}, {FilePath: "LICENSE"}},
			want:      []string{},
			wantNonGo: []string{".golangci.yml", "LICENSE", "go.sum", "internal/repo/README.md"},
		},
		{
			name:       "testdata change seeds only the owning package's tests",
			ranges:     []DiffRange{{FilePath: "internal/service/testdata/fixture.json", StartLine: 1, EndLine: 1}},
			want:       []string{"./internal/service.TestSet", "./internal/service_test.Test", "./internal/service_test.TestCreateUser"},
			wantReason: ReasonTestdata,
			wantNonGo:  []string{"internal/service/testdata/fixture.json"},
		},
		{
			name:   "files skipped by the scanner are ignored",
			ranges: []DiffRange{{FilePath: "vendor/example.com/x/x.go", StartLine: 1, EndLine: 1}, {FilePath: "nested/n.go", StartLine: 1, EndLine: 1}},
			want:   []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindSeeds(idx, tt.ranges)
			if ids := seedIDs(got); !reflect.DeepEqual(ids, tt.want) {
				t.Errorf("seeds =\n%v\nwant\n%v", ids, tt.want)
			}
			for _, s := range got.Seeds {
				if tt.wantReason != "" && s.Reason != tt.wantReason {
					t.Errorf("%s reason = %s, want %s", s.Symbol.ID, s.Reason, tt.wantReason)
				}
				if s.Cause != tt.ranges[0].FilePath {
					t.Errorf("%s cause = %s, want %s", s.Symbol.ID, s.Cause, tt.ranges[0].FilePath)
				}
			}
			if !reflect.DeepEqual(got.NonGoFiles, tt.wantNonGo) {
				t.Errorf("NonGoFiles = %v, want %v", got.NonGoFiles, tt.wantNonGo)
			}
		})
	}
}

// TestFindSeeds_FirstReasonWins checks that a symbol seeded by several
// ranges keeps the reason of the first range, independent of map order.
func TestFindSeeds_FirstReasonWins(t *testing.T) {
	idx := scanFixture(t)
	const user = "internal/service/user.go"
	l := lineOf(t, user, "return s.repo.Create")
	for range 5 {
		got := FindSeeds(idx, []DiffRange{
			{FilePath: user, StartLine: l, EndLine: l},
			{FilePath: user, StartLine: lineOf(t, user, `"context"`), EndLine: lineOf(t, user, `"context"`)},
		})
		for _, s := range got.Seeds {
			want := ReasonTopLevel
			if strings.HasSuffix(s.Symbol.ID, ".Create") {
				want = ReasonChangedLines
			}
			if s.Reason != want {
				t.Fatalf("%s reason = %s, want %s", s.Symbol.ID, s.Reason, want)
			}
		}
	}
}

func TestOverlapping(t *testing.T) {
	syms := []*SymbolNode{{ID: "a", StartLine: 3, EndLine: 5}, {ID: "b", StartLine: 6, EndLine: 8}, {ID: "c", StartLine: 12, EndLine: 12}}
	tests := []struct {
		start, end  int
		wantIDs     []string
		wantCovered bool
	}{
		{start: 4, end: 4, wantIDs: []string{"a"}, wantCovered: true},
		{start: 3, end: 8, wantIDs: []string{"a", "b"}, wantCovered: true},
		{start: 8, end: 12, wantIDs: []string{"b", "c"}, wantCovered: false},
		{start: 1, end: 2, wantIDs: nil, wantCovered: false},
		{start: 13, end: 14, wantIDs: nil, wantCovered: false},
	}
	for _, tt := range tests {
		hit, covered := overlapping(syms, tt.start, tt.end)
		var ids []string
		for _, s := range hit {
			ids = append(ids, s.ID)
		}
		if !reflect.DeepEqual(ids, tt.wantIDs) || covered != tt.wantCovered {
			t.Errorf("overlapping(%d, %d) = %v, %v; want %v, %v", tt.start, tt.end, ids, covered, tt.wantIDs, tt.wantCovered)
		}
	}
}
