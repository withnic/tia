package tia

import (
	"bytes"
	"context"
	"go/parser"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

const appRoot = "testdata/app"

func scanApp(t *testing.T, hubs []string) *RepoIndex {
	t.Helper()
	idx, err := Scan(context.Background(), ScanOptions{Root: appRoot, HubPatterns: hubs})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	return idx
}

// appLine returns the line of substr in an app fixture file.
func appLine(t *testing.T, rel, substr string) DiffRange {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(appRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(data, []byte(substr))
	if i < 0 {
		t.Fatalf("%q not found in %s", substr, rel)
	}
	l := bytes.Count(data[:i], []byte("\n")) + 1
	return DiffRange{FilePath: rel, StartLine: l, EndLine: l}
}

// selected renders the selection as "Name:reason", sorted.
func selected(sel *Selection) []string {
	out := make([]string, 0, len(sel.Tests))
	for _, t := range sel.Tests {
		out = append(out, t.Symbol.BaseName+":"+string(t.Reason))
	}
	sort.Strings(out)
	return out
}

func TestAnalyze(t *testing.T) {
	hubs := []string{"NewServer", "Initialize*"}
	repoCreate := appLine(t, "internal/repo/repo.go", "r.rows = append")
	tests := []struct {
		name      string
		ranges    []DiffRange
		hubs      []string
		want      []string
		wantHubs  []string
		wantFallb string
	}{
		{
			// Same-named Create in package order must not leak: neither
			// order nor its tests import repo, and order.Create's signature
			// differs from the UserService interface method.
			name:   "repo.Create without hubs reaches every caller",
			ranges: []DiffRange{repoCreate},
			want: []string{
				"TestRepoCreate:reference", "TestRepoList:reference", "TestServerAPI:reference",
				"TestServiceCreate:reference", "TestUsersAPI:reference",
			},
		},
		{
			name:   "order.Create does not select user tests (false positive guard)",
			ranges: []DiffRange{appLine(t, "internal/order/order.go", "s.n++")},
			want:   []string{"TestOrderCreate:reference", "TestOrdersAPI:reference", "TestServerAPI:reference"},
		},
		{
			name:     "hubs stop the traversal",
			ranges:   []DiffRange{appLine(t, "internal/service/service.go", "func NewUserService")},
			hubs:     hubs,
			want:     []string{"TestServiceCreate:reference"},
			wantHubs: []string{"./internal/di.InitializeUserHandler"},
		},
		{
			name:     "method change with hubs",
			ranges:   []DiffRange{repoCreate},
			hubs:     hubs,
			want:     []string{"TestRepoCreate:reference", "TestRepoList:reference", "TestServiceCreate:reference", "TestUsersAPI:reference"},
			wantHubs: []string{"./internal/di.InitializeUserHandler", "./internal/server.NewServer"},
		},
		{
			name:   "changed test file selects that test directly",
			ranges: []DiffRange{appLine(t, "internal/order/order_test.go", "order.NewOrderService().Create")},
			hubs:   hubs,
			want:   []string{"TestOrderCreate:changed"},
		},
		{
			name:   "changed test helper selects the tests using it",
			ranges: []DiffRange{appLine(t, "internal/repo/helper_integration_test.go", "func seedUsers")},
			want:   []string{"TestRepoList:reference"},
		},
		{
			name:     ".sql next to a Go package seeds it and propagates to the DB layer tests",
			ranges:   []DiffRange{{FilePath: "internal/repo/schema.sql", StartLine: 1, EndLine: 1}},
			hubs:     hubs,
			want:     []string{"TestRepoCreate:changed", "TestRepoList:changed", "TestServiceCreate:reference", "TestUsersAPI:reference"},
			wantHubs: []string{"./internal/di.InitializeUserHandler", "./internal/server.NewServer"},
		},
		{
			name:      "migration without a Go package falls back to every integration test",
			ranges:    []DiffRange{repoCreate, {FilePath: "migrations/001_init.sql", StartLine: 1, EndLine: 1}},
			want:      allAppIntegrationTests("fallback"),
			wantFallb: "migrations/001_init.sql",
		},
		{
			name:      "go.mod change falls back to every integration test",
			ranges:    []DiffRange{{FilePath: "go.mod", StartLine: 3, EndLine: 3}},
			want:      allAppIntegrationTests("fallback"),
			wantFallb: "go.mod",
		},
		{
			name:   "documentation only",
			ranges: []DiffRange{{FilePath: "README.md", StartLine: 1, EndLine: 1}},
			want:   []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := scanApp(t, tt.hubs)
			sel := Analyze(idx, FindSeeds(idx, tt.ranges))
			if got := selected(sel); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tests =\n%v\nwant\n%v", got, tt.want)
			}
			if !reflect.DeepEqual(sel.StoppedAtHubs, tt.wantHubs) {
				t.Errorf("StoppedAtHubs = %v, want %v", sel.StoppedAtHubs, tt.wantHubs)
			}
			if sel.Fallback != tt.wantFallb {
				t.Errorf("Fallback = %q, want %q", sel.Fallback, tt.wantFallb)
			}
		})
	}
}

func allAppIntegrationTests(reason string) []string {
	names := []string{
		"TestHandlerSmoke", "TestOrderCreate", "TestOrdersAPI", "TestRepoCreate",
		"TestRepoList", "TestServerAPI", "TestServiceCreate", "TestUsersAPI",
	}
	for i := range names {
		names[i] += ":" + reason
	}
	return names
}

// TestAnalyze_Path checks the explanation chain from test back to seed.
func TestAnalyze_Path(t *testing.T) {
	idx := scanApp(t, nil)
	sel := Analyze(idx, FindSeeds(idx, []DiffRange{appLine(t, "internal/repo/repo.go", "r.rows = append")}))
	for _, ts := range sel.Tests {
		if ts.Symbol.BaseName != "TestUsersAPI" {
			continue
		}
		// Shortest chain: Handle may call repo.Create through a value
		// obtained from service (which imports repo), so it is one hop.
		want := []string{
			"./test/e2e.TestUsersAPI",
			"./internal/handler.(*UserHandler).Handle",
			"./internal/repo.(*UserRepo).Create",
		}
		if !reflect.DeepEqual(ts.Path, want) {
			t.Errorf("Path = %v, want %v", ts.Path, want)
		}
		return
	}
	t.Fatal("TestUsersAPI not selected")
}

func TestScan_Signatures(t *testing.T) {
	idx := scanApp(t, nil)
	repoCreate := symbolByID(t, idx, "./internal/repo.(*UserRepo).Create")
	if want := "(context.Context,string)(error)"; repoCreate.Signature != want {
		t.Errorf("repo Create signature = %q, want %q", repoCreate.Signature, want)
	}
	iface := symbolByID(t, idx, "./internal/service.UserService")
	if want := map[string]string{"Create": "(context.Context,string)(error)"}; !reflect.DeepEqual(iface.IfaceMethods, want) {
		t.Errorf("UserService methods = %v, want %v", iface.IfaceMethods, want)
	}
	handle := symbolByID(t, idx, "./internal/server.(*Server).Serve")
	if want := "(string,string)(error)"; handle.Signature != want {
		t.Errorf("Serve signature = %q, want %q", handle.Signature, want)
	}
}

func TestRenderType(t *testing.T) {
	tests := []struct {
		src, want string
	}{
		{src: "func(a, b int, c *pkg.T, d ...string) (map[string][]T, error)", want: "func(int,int,*{pkg}.T,...string)(map[string][]{.}T,error)"},
		{src: "func(ch <-chan int, out chan<- [4]byte, x any, f func() struct{})", want: "func(<-chan int,chan<- [4]byte,interface{},func()(struct{}))()"},
		{src: "func(x struct{ A int })", want: ""},
		{src: "func(x List[int])", want: ""},
		{src: "func(x [n]int)", want: ""},
	}
	for _, tt := range tests {
		expr, err := parser.ParseExpr(tt.src)
		if err != nil {
			t.Fatal(err)
		}
		got, err := renderType(expr)
		if err != nil {
			got = ""
		}
		if got != tt.want {
			t.Errorf("renderType(%s) = %q, want %q", tt.src, got, tt.want)
		}
	}
	if got := qualifySignature("({.}T,*{pkg}.U)()", "ex.com/self", map[string]string{"pkg": "ex.com/p"}); got != "(ex.com/self.T,*ex.com/p.U)()" {
		t.Errorf("qualifySignature = %q", got)
	}
	if got := qualifySignature("({missing}.U)()", "ex.com/self", nil); got != "" {
		t.Errorf("qualifySignature(unknown alias) = %q, want empty", got)
	}
	if got := qualifySignature("({.}T)()", "ex.com/self", map[string]string{".ex.com/d": "ex.com/d"}); got != "" {
		t.Errorf("qualifySignature(dot import) = %q, want empty", got)
	}
}