package tia

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseUnifiedDiff(t *testing.T) {
	tests := []struct {
		name string
		diff string
		want []DiffRange
	}{
		{
			name: "modified lines, count omitted and multiple hunks",
			diff: `diff --git a/svc/user.go b/svc/user.go
index 1111111..2222222 100644
--- a/svc/user.go
+++ b/svc/user.go
@@ -10,2 +10,3 @@ func A() {
-old
-old
+new
+new
+new
@@ -30 +31 @@ func B() {
-x
+y
`,
			want: []DiffRange{
				{FilePath: "svc/user.go", StartLine: 10, EndLine: 12},
				{FilePath: "svc/user.go", StartLine: 31, EndLine: 31},
			},
		},
		{
			name: "pure deletion touches the surrounding lines",
			diff: `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -5,3 +4,0 @@
-gone
-gone
-gone
@@ -1 +0,0 @@
-first line removed
`,
			want: []DiffRange{
				{FilePath: "a.go", StartLine: 1, EndLine: 1},
				{FilePath: "a.go", StartLine: 4, EndLine: 5},
			},
		},
		{
			name: "new file, deleted file and hunk lines that look like headers",
			diff: `diff --git a/new.go b/new.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package x
+++ this line starts with three pluses
+--- and this one with dashes
diff --git a/old.go b/old.go
deleted file mode 100644
index 4444444..0000000
--- a/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package x
--- removed line starting with dashes
`,
			want: []DiffRange{
				{FilePath: "new.go", StartLine: 1, EndLine: 3},
				{FilePath: "old.go", Deleted: true},
			},
		},
		{
			name: "binary file, mode change, quoted path and no newline marker",
			diff: `diff --git a/img/logo.png b/img/logo.png
index 5555555..6666666 100644
Binary files a/img/logo.png and b/img/logo.png differ
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
diff --git "a/dir/t\303\244b\tx.go" "b/dir/t\303\244b\tx.go"
--- "a/dir/t\303\244b\tx.go"
+++ "b/dir/t\303\244b\tx.go"
@@ -2 +2 @@
-a
\ No newline at end of file
+b
\ No newline at end of file
`,
			want: []DiffRange{
				{FilePath: "dir/täb\tx.go", StartLine: 2, EndLine: 2},
				{FilePath: "img/logo.png"},
				{FilePath: "run.sh"},
			},
		},
		{name: "empty diff", diff: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUnifiedDiff(strings.NewReader(tt.diff))
			if err != nil {
				t.Fatalf("ParseUnifiedDiff() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseUnifiedDiff() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestParseUnifiedDiff_Errors(t *testing.T) {
	tests := []string{
		"diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 @@\n",
		"diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +a,1 @@\n",
		"diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1,z @@\n",
		"diff --git a/x b/x\n--- \"a/unterminated\n",
	}
	for _, in := range tests {
		if _, err := ParseUnifiedDiff(strings.NewReader(in)); err == nil {
			t.Errorf("ParseUnifiedDiff(%q) error = nil", in)
		}
	}
}

func TestDiffRange_WholeFile(t *testing.T) {
	if !(DiffRange{FilePath: "a"}).WholeFile() || (DiffRange{FilePath: "a", StartLine: 1, EndLine: 1}).WholeFile() {
		t.Error("WholeFile mismatch")
	}
}

// git runs git in dir with a fixed identity.
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

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestChangedRanges_Git runs real git commands in a temporary repository:
// committed branch changes, staged and unstaged edits and untracked files,
// with the module in a subdirectory (paths are relative to it).
func TestChangedRanges_Git(t *testing.T) {
	if testing.Short() {
		t.Skip("runs git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	mod := filepath.Join(repo, "app")
	git(t, repo, "init", "-q", "-b", "main")
	writeFile(t, mod, "go.mod", "module example.com/app\n")
	writeFile(t, mod, "a.go", "package app\n\nfunc A() int {\n\treturn 1\n}\n")
	writeFile(t, mod, "b.go", "package app\n\nfunc B() {}\n")
	writeFile(t, mod, "gone.go", "package app\n")
	writeFile(t, repo, "outside.txt", "outside the module\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "base")

	git(t, repo, "checkout", "-q", "-b", "feature")
	writeFile(t, mod, "a.go", "package app\n\nfunc A() int {\n\treturn 2\n}\n")
	git(t, repo, "rm", "-q", "app/gone.go")
	writeFile(t, repo, "outside.txt", "changed\n")
	git(t, repo, "commit", "-q", "-am", "feature")

	// Uncommitted: shift a.go down by two lines (unstaged), stage an edit
	// to b.go, and add an untracked file.
	writeFile(t, mod, "a.go", "package app\n\n// A doc.\n//\nfunc A() int {\n\treturn 2\n}\n")
	writeFile(t, mod, "b.go", "package app\n\nfunc B() { _ = 1 }\n")
	git(t, repo, "add", "app/b.go")
	writeFile(t, mod, "new.go", "package app\n")

	ctx := context.Background()
	t.Run("committed only", func(t *testing.T) {
		got, err := ChangedRanges(ctx, DiffOptions{Dir: mod, Base: "main"})
		if err != nil {
			t.Fatal(err)
		}
		want := []DiffRange{
			{FilePath: "a.go", StartLine: 4, EndLine: 4},
			{FilePath: "gone.go", Deleted: true},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ChangedRanges() =\n%+v\nwant\n%+v", got, want)
		}
	})
	t.Run("with dirty working tree", func(t *testing.T) {
		got, err := ChangedRanges(ctx, DiffOptions{Dir: mod, Base: "main", Dirty: true})
		if err != nil {
			t.Fatal(err)
		}
		// Line numbers refer to the working tree: "return 2" is now line 6.
		want := []DiffRange{
			{FilePath: "a.go", StartLine: 3, EndLine: 4},
			{FilePath: "a.go", StartLine: 6, EndLine: 6},
			{FilePath: "b.go", StartLine: 3, EndLine: 3},
			{FilePath: "gone.go", Deleted: true},
			{FilePath: "new.go"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ChangedRanges() =\n%+v\nwant\n%+v", got, want)
		}
	})
	t.Run("errors", func(t *testing.T) {
		if _, err := ChangedRanges(ctx, DiffOptions{Dir: mod}); err == nil {
			t.Error("empty base: error = nil")
		}
		_, err := ChangedRanges(ctx, DiffOptions{Dir: mod, Base: "no-such-branch"})
		if err == nil || !strings.Contains(err.Error(), "git merge-base") {
			t.Errorf("unknown base: error = %v", err)
		}
	})
}
