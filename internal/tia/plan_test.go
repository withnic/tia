package tia

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// copyTree copies src into dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func replaceInFile(t *testing.T, path, old, repl string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(old)) {
		t.Fatalf("%q not found in %s", old, path)
	}
	if err := os.WriteFile(path, bytes.Replace(data, []byte(old), []byte(repl), 1), 0o600); err != nil {
		t.Fatal(err)
	}
}

// setupAppRepo copies the app fixture into a git repository whose main
// branch holds the original code and checks out a feature branch.
func setupAppRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	copyTree(t, appRoot, dir)
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "checkout", "-q", "-b", "feature")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOPROXY", "off")
	return dir
}

// TestPlan_EndToEnd runs the whole pipeline in a temporary git repository:
// a committed change to repo.Create selects exactly the affected
// integration tests, which are then executed (and fail once a bug is
// introduced).
func TestPlan_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs git and go test")
	}
	dir := setupAppRepo(t)
	repoFile := filepath.Join(dir, "internal", "repo", "repo.go")
	replaceInFile(t, repoFile, "r.rows = append(r.rows, name)", "r.rows = append(r.rows, name+\"\")")
	git(t, dir, "commit", "-q", "-am", "tweak repo.Create")

	opts := PlanOptions{
		Root:        dir,
		Base:        "main",
		Dirty:       true,
		HubPatterns: []string{"NewServer", "Initialize*"},
	}
	ctx := context.Background()
	res, err := Plan(ctx, opts)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", res.Warnings)
	}
	wantCmd := "go test -tags=integration -p 1 -count=1 -run '^(TestRepoCreate|TestRepoList|TestServiceCreate|TestUsersAPI)$' " +
		"./internal/repo ./internal/service ./test/e2e"
	if got := FormatCommand(res.Command); got != wantCmd {
		t.Fatalf("command =\n%s\nwant\n%s", got, wantCmd)
	}

	var out bytes.Buffer
	if err := Exec(ctx, dir, append(res.Command[:1:1], append([]string{"-v"}, res.Command[1:]...)...), &out, &out); err != nil {
		t.Fatalf("Exec() error = %v\n%s", err, out.String())
	}
	ran := strings.Count(out.String(), "--- PASS: ")
	if ran != 4 || strings.Contains(out.String(), "TestOrderCreate") || strings.Contains(out.String(), "TestRepoUnit") {
		t.Errorf("expected exactly the 4 selected tests to run:\n%s", out.String())
	}

	// An uncommitted bug is picked up with Dirty and makes the run fail.
	replaceInFile(t, repoFile, "return nil\n}\n\nfunc (r *UserRepo) List", "return errors.New(\"boom\")\n}\n\nfunc (r *UserRepo) List")
	replaceInFile(t, repoFile, `import "context"`, "import (\n\t\"context\"\n\t\"errors\"\n)")
	res, err = Plan(ctx, opts)
	if err != nil {
		t.Fatalf("Plan(dirty) error = %v", err)
	}
	out.Reset()
	err = Exec(ctx, dir, res.Command, &out, &out)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 1 {
		t.Errorf("Exec(buggy) error = %v, want exit status 1\n%s", err, out.String())
	}

	// Without Dirty the bug is ignored, with a warning.
	opts.Dirty = false
	res, err = Plan(ctx, opts)
	if err != nil {
		t.Fatalf("Plan(clean) error = %v", err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "uncommitted changes") {
		t.Errorf("Warnings = %v, want the uncommitted-changes warning", res.Warnings)
	}
}

func TestPlan_Scenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("runs git")
	}
	tests := []struct {
		name     string
		change   func(t *testing.T, dir string)
		wantCmd  string
		wantWarn string
	}{
		{
			name:   "no changes",
			change: func(*testing.T, string) {},
		},
		{
			name: "untracked integration test file is selected",
			change: func(t *testing.T, dir string) {
				writeFile(t, dir, "internal/order/new_integration_test.go", "package order\n\nimport \"testing\"\n\nfunc TestNewThing(t *testing.T) {}\n")
			},
			wantCmd: "go test -tags=integration -p 1 -count=1 -run '^(TestNewThing)$' ./internal/order",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := setupAppRepo(t)
			tt.change(t, dir)
			opts := PlanOptions{Root: dir, Base: "main", Dirty: true}
			res, err := Plan(context.Background(), opts)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			got := ""
			if res.Command != nil {
				got = FormatCommand(res.Command)
			}
			if got != tt.wantCmd {
				t.Errorf("command =\n%s\nwant\n%s", got, tt.wantCmd)
			}
			warn := strings.Join(res.Warnings, "\n")
			if (tt.wantWarn == "") != (warn == "") || !strings.Contains(warn, tt.wantWarn) {
				t.Errorf("Warnings = %q, want containing %q", warn, tt.wantWarn)
			}
		})
	}
}

func TestFindModuleRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module x\n")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	got, err := FindModuleRoot(sub)
	if err != nil || got != root {
		t.Errorf("FindModuleRoot() = %q, %v; want %q", got, err, root)
	}
	if _, err := FindModuleRoot(filepath.VolumeName(root) + string(filepath.Separator)); !errors.Is(err, ErrNoModule) {
		t.Errorf("FindModuleRoot(/) error = %v, want ErrNoModule", err)
	}
}
