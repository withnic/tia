package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRun_Flags(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantOut    string
		wantErrOut string
	}{
		{name: "version", args: []string{"-version"}, wantCode: exitOK, wantOut: "tia dev\n"},
		{name: "help", args: []string{"-h"}, wantCode: exitOK, wantErrOut: "-hub-exclude"},
		{name: "bad flag", args: []string{"-nope"}, wantCode: exitUsage, wantErrOut: "flag provided but not defined"},
		{name: "extra args", args: []string{"./..."}, wantCode: exitUsage, wantErrOut: "unexpected arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := runCLI(t, tt.args...)
			if code != tt.wantCode || !strings.Contains(out, tt.wantOut) || !strings.Contains(errOut, tt.wantErrOut) {
				t.Errorf("run(%v) = %d\nstdout: %s\nstderr: %s\nwant %d, stdout ~ %q, stderr ~ %q",
					tt.args, code, out, errOut, tt.wantCode, tt.wantOut, tt.wantErrOut)
			}
		})
	}
}

func TestSplitList(t *testing.T) {
	got := splitList(" NewServer, ,Initialize* ,")
	if strings.Join(got, "|") != "NewServer|Initialize*" {
		t.Errorf("splitList() = %q", got)
	}
	if splitList("") != nil {
		t.Error(`splitList("") != nil`)
	}
}

func TestRun_NoModule(t *testing.T) {
	t.Chdir(t.TempDir())
	code, _, errOut := runCLI(t)
	if code != exitError || !strings.Contains(errOut, "go.mod") {
		t.Errorf("run() = %d, stderr: %s", code, errOut)
	}
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=tia", "GIT_AUTHOR_EMAIL=tia@example.invalid",
		"GIT_COMMITTER_NAME=tia", "GIT_COMMITTER_EMAIL=tia@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// app is a small module: svc depends on store; other is unrelated.
var app = map[string]string{
	"go.mod": "module example.com/app\n\ngo 1.26\n",
	"store/store.go": `package store

type Store struct{}

func NewStore() *Store { return &Store{} }

func (s *Store) Get() string { return "world" }
`,
	"svc/svc.go": `package svc

import "example.com/app/store"

type Svc struct{ st *store.Store }

func NewSvc(st *store.Store) *Svc { return &Svc{st: st} }

func (s *Svc) Hello() string { return "hello " + s.st.Get() }
`,
	"svc/svc_integration_test.go": `package svc

import (
	"testing"

	"example.com/app/store"
)

func TestHello(t *testing.T) {
	if got := NewSvc(store.NewStore()).Hello(); got != "hello world" {
		t.Fatalf("Hello() = %q", got)
	}
}
`,
	"other/other.go":                  "package other\n\nfunc Other() int { return 1 }\n",
	"other/other_integration_test.go": "package other\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) { _ = Other() }\n",
}

// TestRun_EndToEnd drives the CLI against a temporary git repository: a
// change on a branch selects, prints and executes only the affected test.
func TestRun_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs git and go test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for rel, content := range app {
		write(t, dir, rel, content)
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOPROXY", "off")
	t.Chdir(dir)
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "checkout", "-q", "-b", "feature")

	// Uncommitted change (-dirty defaults to true).
	write(t, dir, "store/store.go", strings.Replace(app["store/store.go"], `return "world"`, `return "wor" + "ld"`, 1))

	code, out, errOut := runCLI(t, "-v")
	wantCmd := "go test -tags=integration -p 1 -count=1 -run '^(TestHello)$' ./svc\n"
	if code != exitOK || out != wantCmd {
		t.Fatalf("run(-v) = %d\nstdout: %s\nstderr: %s\nwant stdout %q", code, out, errOut, wantCmd)
	}
	for _, want := range []string{
		"seed ./store.(*Store).Get",
		"test ./svc.TestHello (reference: ./svc.TestHello <- ./svc.(*Svc).Hello <- ./store.(*Store).Get)",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errOut)
		}
	}

	code, out, errOut = runCLI(t, "-exec")
	if code != exitOK || !strings.Contains(out, "ok  \texample.com/app/svc") || strings.Contains(errOut, "warning") {
		t.Fatalf("run(-exec) = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}

	// A breaking change propagates go test's exit status.
	write(t, dir, "store/store.go", strings.Replace(app["store/store.go"], `return "world"`, `return "moon"`, 1))
	if code, out, errOut = runCLI(t, "-exec"); code != 1 {
		t.Fatalf("run(-exec, broken) = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}

	// Back to the base state: nothing to run.
	git(t, dir, "checkout", "-q", "--", ".")
	code, out, errOut = runCLI(t)
	if code != exitOK || out != "" || !strings.Contains(errOut, "no affected integration tests") {
		t.Fatalf("run(clean) = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
}
