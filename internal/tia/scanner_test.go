package tia

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const fixtureRoot = "testdata/repo"

var hubPatterns = []string{"NewServer", "Initialize*"}

var fixtureScan struct {
	once sync.Once
	idx  *RepoIndex
	err  error
}

// scanFixture scans testdata/repo once per test binary.
func scanFixture(t *testing.T) *RepoIndex {
	t.Helper()
	fixtureScan.once.Do(func() {
		fixtureScan.idx, fixtureScan.err = Scan(context.Background(), ScanOptions{Root: fixtureRoot, HubPatterns: hubPatterns})
	})
	if fixtureScan.err != nil {
		t.Fatalf("Scan() error = %v", fixtureScan.err)
	}
	return fixtureScan.idx
}

func symbolByID(t *testing.T, idx *RepoIndex, id string) SymbolNode {
	t.Helper()
	for _, s := range idx.Symbols {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("symbol %q not found", id)
	return SymbolNode{}
}

// lineOf returns the 1-based line of the first occurrence of substr in the
// fixture file rel.
func lineOf(t *testing.T, rel, substr string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(data, []byte(substr))
	if i < 0 {
		t.Fatalf("%q not found in %s", substr, rel)
	}
	return bytes.Count(data[:i], []byte("\n")) + 1
}

func TestScan_Files(t *testing.T) {
	idx := scanFixture(t)
	if idx.ModulePath != "example.com/shop" {
		t.Errorf("ModulePath = %q, want example.com/shop", idx.ModulePath)
	}
	var got []string
	for _, f := range idx.Files {
		got = append(got, f.Path)
	}
	// vendor, testdata, _ignored, .hidden and the nested module are skipped.
	want := []string{
		"internal/broken/broken.go",
		"internal/broken/ok.go",
		"internal/db/db.go",
		"internal/repo/repo.go",
		"internal/repo/repo_db_test.go",
		"internal/repo/repo_unit_test.go",
		"internal/server/server.go",
		"internal/service/user.go",
		"internal/service/user_integration_test.go",
		"internal/service/user_test.go",
		"main.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Files =\n%v\nwant\n%v", got, want)
	}

	integration := map[string]bool{}
	for _, f := range idx.Files {
		if f.IsIntegration {
			integration[f.Path] = true
		}
		if (f.ParseError != "") != (f.Path == "internal/broken/broken.go") {
			t.Errorf("%s ParseError = %q", f.Path, f.ParseError)
		}
	}
	// By tag (integration && linux) and by suffix; !integration is not.
	wantInt := map[string]bool{"internal/repo/repo_db_test.go": true, "internal/service/user_integration_test.go": true}
	if !reflect.DeepEqual(integration, wantInt) {
		t.Errorf("integration files = %v, want %v", integration, wantInt)
	}
}

func TestScan_Symbols(t *testing.T) {
	idx := scanFixture(t)
	const user = "internal/service/user.go"
	const itest = "internal/service/user_integration_test.go"
	tests := []struct {
		id   string
		want SymbolNode
	}{
		{
			id: "./internal/service.UserService",
			want: SymbolNode{
				Kind: KindInterface, BaseName: "UserService",
				StartLine: lineOf(t, user, "// UserService"), EndLine: lineOf(t, user, "type userServiceImpl") - 2,
				Refs: []string{"Context", "Create", "context", "ctx", "name"},
			},
		},
		{
			id: "./internal/service.NewUserService",
			want: SymbolNode{
				Kind: KindFunc, BaseName: "NewUserService",
				StartLine: lineOf(t, user, "// NewUserService"), EndLine: lineOf(t, user, "func (s *userServiceImpl)") - 2,
				Refs: []string{"UserRepo", "UserService", "r", "repo", "userServiceImpl"},
			},
		},
		{
			id: "./internal/service.(*userServiceImpl).Create",
			want: SymbolNode{
				Kind: KindMethod, BaseName: "Create", ReceiverType: "*userServiceImpl",
				StartLine: lineOf(t, user, "func (s *userServiceImpl)"), EndLine: lineOf(t, user, "func (s *userServiceImpl)") + 2,
				Refs: []string{"Context", "context", "ctx", "name", "repo", "s", "userServiceImpl"},
			},
		},
		{
			id: "./internal/service.(*Set).Add",
			want: SymbolNode{
				Kind: KindMethod, BaseName: "Add", ReceiverType: "*Set",
				StartLine: lineOf(t, user, "func (s *Set[T])"), EndLine: lineOf(t, user, "func (s *Set[T])"),
				Refs: []string{"Set", "T", "m", "s", "v"},
			},
		},
		{
			id: "./internal/service.Set.Len",
			want: SymbolNode{
				Kind: KindMethod, BaseName: "Len", ReceiverType: "Set",
				StartLine: lineOf(t, user, "func (s Set[T])"), EndLine: lineOf(t, user, "func (s Set[T])"),
				Refs: []string{"Set", "T", "m", "s"},
			},
		},
		{
			// Constants of a group share the group's range (iota).
			id: "./internal/service.StatusInactive",
			want: SymbolNode{
				Kind: KindConst, BaseName: "StatusInactive",
				StartLine: lineOf(t, user, "const ("), EndLine: lineOf(t, user, "var MaxRetry") - 2,
				Refs: []string{},
			},
		},
		{
			id: "./internal/service.Timeout",
			want: SymbolNode{
				Kind: KindVar, BaseName: "Timeout",
				StartLine: lineOf(t, user, "var MaxRetry"), EndLine: lineOf(t, user, "var MaxRetry"),
				Refs: []string{"DefaultTimeout", "dbx"},
			},
		},
		{
			id: "./internal/service.Alias",
			want: SymbolNode{
				Kind: KindType, BaseName: "Alias",
				StartLine: lineOf(t, user, "type Alias"), EndLine: lineOf(t, user, "type Alias"),
				Refs: []string{"ID"},
			},
		},
		{
			id: "./internal/service_test.TestCreateUser",
			want: SymbolNode{
				Kind: KindFunc, BaseName: "TestCreateUser", IsTest: true, IsIntegration: true,
				StartLine: lineOf(t, itest, "func TestCreateUser"), EndLine: lineOf(t, itest, "func TestCreateUser"),
				Refs: []string{"T", "helperCreate", "t", "testing"},
			},
		},
		{
			// "Test" alone is a valid test name.
			id: "./internal/service_test.Test",
			want: SymbolNode{
				Kind: KindFunc, BaseName: "Test", IsTest: true, IsIntegration: true,
				StartLine: lineOf(t, itest, "func Test(t"), EndLine: lineOf(t, itest, "func Test(t"),
				Refs: []string{"T", "t", "testing"},
			},
		},
		{
			id: "./internal/server.NewServer",
			want: SymbolNode{
				Kind: KindFunc, BaseName: "NewServer", IsHub: true,
				StartLine: lineOf(t, "internal/server/server.go", "func NewServer"), EndLine: lineOf(t, "internal/server/server.go", "func NewServer"),
				Refs: []string{"NewUserService", "Server", "service", "svc"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			got := symbolByID(t, idx, tt.id)
			// Signatures are covered by TestScan_Signatures.
			got.Signature, got.IfaceMethods, got.IfaceEmbeds = "", nil, false
			want := tt.want
			want.ID = tt.id
			want.FilePath = got.FilePath
			want.PkgDir = PkgDirOf(got.FilePath)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("symbol =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

func TestScan_TestDetectionAndHubs(t *testing.T) {
	idx := scanFixture(t)
	var tests, hubs []string
	for _, s := range idx.Symbols {
		if s.IsTest {
			tests = append(tests, s.ID)
		}
		if s.IsHub {
			hubs = append(hubs, s.ID)
		}
	}
	// TestMain, Testlowercase and helpers are not tests.
	wantTests := []string{
		"./internal/repo.TestRepoCreate",
		"./internal/repo.TestRepoUnit",
		"./internal/service.TestSet",
		"./internal/service_test.Test",
		"./internal/service_test.TestCreateUser",
	}
	if !reflect.DeepEqual(tests, wantTests) {
		t.Errorf("tests = %v, want %v", tests, wantTests)
	}
	if want := []string{"./internal/server.InitializeRoutes", "./internal/server.NewServer"}; !reflect.DeepEqual(hubs, want) {
		t.Errorf("hubs = %v, want %v", hubs, want)
	}
}

func TestScan_UniqueIDs(t *testing.T) {
	idx := scanFixture(t)
	seen := map[string]bool{}
	var inits []string
	for _, s := range idx.Symbols {
		if seen[s.ID] {
			t.Errorf("duplicate ID %q", s.ID)
		}
		seen[s.ID] = true
		if s.BaseName == "init" {
			inits = append(inits, s.ID)
		}
	}
	const user = "internal/service/user.go"
	first := lineOf(t, user, "func init()")
	want := []string{
		"./internal/service.init@internal/service/user.go:" + strconv.Itoa(first),
		"./internal/service.init@internal/service/user.go:" + strconv.Itoa(first+2),
	}
	if !reflect.DeepEqual(inits, want) {
		t.Errorf("init IDs = %v, want %v", inits, want)
	}
}

func TestScan_ImportMap(t *testing.T) {
	idx := scanFixture(t)
	tests := []struct {
		file string
		want map[string]string
	}{
		{
			file: "internal/service/user.go",
			want: map[string]string{
				"context": "context",
				"dbx":     "example.com/shop/internal/db",
				"repo":    "example.com/shop/internal/repo",
			},
		},
		{
			// In-module imports use the real package name ("database" in
			// directory db); external ones are guessed; dot imports are
			// keyed "."+path; blank imports are dropped.
			file: "internal/server/server.go",
			want: map[string]string{
				".strings": "strings",
				"database": "example.com/shop/internal/db",
				"service":  "example.com/shop/internal/service",
				"pgx":      "github.com/jackc/pgx/v5",
				"yaml":     "gopkg.in/yaml.v3",
				"sqlite3":  "github.com/mattn/go-sqlite3",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			got := idx.ImportMap(tt.file)
			if got.FilePath != tt.file || !reflect.DeepEqual(got.Imports, tt.want) {
				t.Errorf("ImportMap = %+v, want %v", got, tt.want)
			}
		})
	}
}

func TestScan_Deterministic(t *testing.T) {
	a, err := Scan(context.Background(), ScanOptions{Root: fixtureRoot, Workers: 1, HubPatterns: hubPatterns})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Scan(context.Background(), ScanOptions{Root: fixtureRoot, Workers: 8, HubPatterns: hubPatterns})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("scans with 1 and 8 workers differ")
	}
}

// TestScan_PartialASTPositions guards against positions of a partial AST
// (syntax error) resolving into another file of the shared FileSet, which
// made line numbers depend on parse order.
func TestScan_PartialASTPositions(t *testing.T) {
	for _, workers := range []int{1, 8} {
		for range 20 {
			idx, err := Scan(context.Background(), ScanOptions{Root: fixtureRoot, Workers: workers})
			if err != nil {
				t.Fatal(err)
			}
			s := symbolByID(t, idx, "./internal/broken.Broken")
			if s.StartLine != 3 || s.EndLine != 3 {
				t.Fatalf("workers=%d: Broken lines = %d-%d, want 3-3", workers, s.StartLine, s.EndLine)
			}
		}
	}
}

func TestScan_Errors(t *testing.T) {
	tests := []struct {
		name   string
		opts   ScanOptions
		wantIs error
		want   string
	}{
		{name: "no go.mod", opts: ScanOptions{Root: t.TempDir()}, wantIs: ErrNoModule},
		{name: "bad hub pattern", opts: ScanOptions{Root: fixtureRoot, HubPatterns: []string{"["}}, want: "invalid hub pattern"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Scan(context.Background(), tt.opts)
			if err == nil || (tt.wantIs != nil && !errors.Is(err, tt.wantIs)) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Scan() error = %v, want %v / %q", err, tt.wantIs, tt.want)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, ScanOptions{Root: fixtureRoot}); !errors.Is(err, context.Canceled) {
		t.Errorf("Scan(canceled) error = %v, want context.Canceled", err)
	}
}

func TestReadModulePath(t *testing.T) {
	tests := []struct {
		content, want string
	}{
		{content: "module example.com/a\n", want: "example.com/a"},
		{content: "// c\nmodule   example.com/b // c\ngo 1.27\n", want: "example.com/b"},
		{content: "module \"example.com/c\"\n", want: "example.com/c"},
		{content: "modulex example.com/d\nmodule example.com/e\n", want: "example.com/e"},
		{content: "go 1.27\n", want: ""},
	}
	for _, tt := range tests {
		p := filepath.Join(t.TempDir(), "go.mod")
		if err := os.WriteFile(p, []byte(tt.content), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := readModulePath(p)
		if got != tt.want || (tt.want == "") != (err != nil) {
			t.Errorf("readModulePath(%q) = %q, %v; want %q", tt.content, got, err, tt.want)
		}
	}
}

func TestGuessPackageName(t *testing.T) {
	tests := map[string]string{
		"context":                       "context",
		"net/http":                      "http",
		"github.com/jackc/pgx/v5":       "pgx",
		"gopkg.in/yaml.v3":              "yaml",
		"github.com/mattn/go-sqlite3":   "sqlite3",
		"cloud.google.com/go/spanner":   "spanner",
		"github.com/org/repo/v2/client": "client",
	}
	for in, want := range tests {
		if got := GuessPackageName(in); got != want {
			t.Errorf("GuessPackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPkgDirHelpers(t *testing.T) {
	if got := PkgDirOf("main.go"); got != "." {
		t.Errorf("PkgDirOf(main.go) = %q", got)
	}
	if got := PkgDirOf("a/b/c.go"); got != "./a/b" {
		t.Errorf("PkgDirOf(a/b/c.go) = %q", got)
	}
	tests := []struct{ mod, imp, want string }{
		{mod: "ex.com/m", imp: "ex.com/m", want: "."},
		{mod: "ex.com/m", imp: "ex.com/m/x/y", want: "./x/y"},
		{mod: "ex.com/m", imp: "ex.com/mm", want: ""},
		{mod: "", imp: "ex.com/m", want: ""},
	}
	for _, tt := range tests {
		if got := PkgDirForImport(tt.mod, tt.imp); got != tt.want {
			t.Errorf("PkgDirForImport(%q, %q) = %q, want %q", tt.mod, tt.imp, got, tt.want)
		}
	}
	if KindMethod.String() != "Method" || SymbolKind(99).String() != "SymbolKind(99)" {
		t.Error("SymbolKind.String mismatch")
	}
}
