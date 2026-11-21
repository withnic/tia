package tia

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlan_FullPipeline runs the entire TIA pipeline against a temporary git
// repository: diff → scan → seeds → analyze → command → exec.
func TestPlan_FullPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("runs git and go test")
	}
	dir := setupAppRepo(t)
	repoFile := filepath.Join(dir, "internal", "repo", "repo.go")
	replaceInFile(t, repoFile, "r.rows = append(r.rows, name)", "r.rows = append(r.rows, name+\"\")")
	git(t, dir, "commit", "-q", "-am", "tweak repo.Create")

	ctx := context.Background()
	res, err := Plan(ctx, PlanOptions{
		Root:        dir,
		Base:        "main",
		Dirty:       true,
		HubPatterns: []string{"NewServer", "Initialize*"},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if res.Command == nil {
		t.Fatal("expected a test command, got nil")
	}

	cmd := FormatCommand(res.Command)
	for _, want := range []string{"TestRepoCreate", "TestServiceCreate", "TestUsersAPI"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q lacks %s", cmd, want)
		}
	}
	if strings.Contains(cmd, "TestOrderCreate") {
		t.Error("command should not contain TestOrderCreate (unrelated package)")
	}

	var out bytes.Buffer
	args := append(res.Command[:1:1], append([]string{"-v"}, res.Command[1:]...)...)
	if err := Exec(ctx, dir, args, &out, &out); err != nil {
		t.Fatalf("Exec() error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "PASS") {
		t.Errorf("expected passing tests:\n%s", out.String())
	}
}

// TestPlan_NoChanges verifies that a clean branch produces no command.
func TestPlan_NoChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("runs git")
	}
	dir := setupAppRepo(t)
	res, err := Plan(context.Background(), PlanOptions{Root: dir, Base: "main", Dirty: true})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if res.Command != nil {
		t.Errorf("expected nil command, got %v", FormatCommand(res.Command))
	}
}
