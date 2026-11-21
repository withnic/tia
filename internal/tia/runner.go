package tia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CommandOptions configures BuildCommand.
type CommandOptions struct {
	// Tag is passed as -tags. Empty means DefaultIntegrationTag.
	Tag string
	// Parallel is passed as -p (packages tested in parallel). Values < 1
	// mean 1, which avoids clashes on shared databases.
	Parallel int
}

// BuildCommand returns the go command arguments (without the leading "go")
// that run exactly the selected tests, or nil when nothing is selected:
//
//	test -tags=integration -p 1 -run ^(TestA|TestB)$ ./pkg/a ./pkg/b
//
// Test names and packages are sorted and deduplicated, so the command is
// deterministic. -count=1 disables the test cache, which cannot see changes
// to the external resources integration tests use.
func BuildCommand(sel *Selection, opts CommandOptions) []string {
	if len(sel.Tests) == 0 {
		return nil
	}
	tag := opts.Tag
	if tag == "" {
		tag = DefaultIntegrationTag
	}
	names := make(map[string]bool)
	pkgs := make(map[string]bool)
	for i := range sel.Tests {
		s := &sel.Tests[i].Symbol
		names[regexp.QuoteMeta(s.BaseName)] = true
		pkgs[s.PkgDir] = true
	}
	return append([]string{
		"test",
		"-tags=" + tag,
		"-p", strconv.Itoa(max(opts.Parallel, 1)),
		"-count=1",
		"-run", "^(" + strings.Join(sortedKeys(names), "|") + ")$",
	}, sortedKeys(pkgs)...)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FormatCommand renders `go <args>` as a POSIX shell command line.
func FormatCommand(args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "go")
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./=,:@%+-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ExitError reports a non-zero exit status of the executed go command.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("go test exited with status %d", e.Code) }

// Exec runs `go <args>` in dir, streaming its output. A failing test run is
// reported as *ExitError carrying the exit code.
func Exec(ctx context.Context, dir string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return &ExitError{Code: ee.ExitCode()}
	}
	if err != nil {
		return fmt.Errorf("running go test: %w", err)
	}
	return nil
}
