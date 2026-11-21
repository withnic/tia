// Command tia runs Test Impact Analysis: it maps the changes of a git branch
// to the integration tests they may affect and prints (or runs) a go test
// command for exactly those tests.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/withnic/tia/internal/tia"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// Exit codes. When -exec runs the tests, go test's own exit status is
// returned instead.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

const usageHeader = `tia - select (and run) the integration tests affected by a git diff

Usage:
  tia [flags]

Run it in a Go module. The command is printed to stdout; -exec runs it.

Flags:
`

// run executes the CLI and returns the process exit code.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tia", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usageHeader)
		fs.PrintDefaults()
	}
	base := fs.String("base", "main", "base branch to compare against (changes since the merge base)")
	tag := fs.String("tag", tia.DefaultIntegrationTag, "build tag of integration test files")
	pattern := fs.String("pattern", tia.DefaultIntegrationSuffix, "file name suffix of integration test files")
	dirty := fs.Bool("dirty", true, "include uncommitted and untracked changes")
	execTests := fs.Bool("exec", false, "run the selected tests instead of printing the command")
	parallel := fs.Int("parallel-pkg", 1, "packages tested in parallel (go test -p)")
	hubs := fs.String("hub-exclude", "", "comma-separated hub function name patterns where traversal stops (e.g. NewServer,SetupRouter,Initialize*)")
	verbose := fs.Bool("v", false, "explain why each test was selected")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if *showVersion {
		fmt.Fprintln(stdout, "tia", version)
		return exitOK
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "tia: unexpected arguments %v\n", fs.Args())
		fs.Usage()
		return exitUsage
	}

	root, err := tia.FindModuleRoot(".")
	if err != nil {
		fmt.Fprintf(stderr, "tia: %v\n", err)
		return exitError
	}
	res, err := tia.Plan(ctx, tia.PlanOptions{
		Root:        root,
		Base:        *base,
		Dirty:       *dirty,
		Tag:         *tag,
		Pattern:     *pattern,
		HubPatterns: splitList(*hubs),
		Parallel:    *parallel,
	})
	if err != nil {
		fmt.Fprintf(stderr, "tia: %v\n", err)
		return exitError
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(stderr, "tia: warning: %s\n", w)
	}
	if *verbose {
		explain(stderr, res)
	}
	if res.Command == nil {
		fmt.Fprintln(stderr, "tia: no affected integration tests")
		return exitOK
	}
	if !*execTests {
		fmt.Fprintln(stdout, tia.FormatCommand(res.Command))
		return exitOK
	}

	fmt.Fprintf(stderr, "tia: %s\n", tia.FormatCommand(res.Command))
	if err := tia.Exec(ctx, root, res.Command, stdout, stderr); err != nil {
		var ee *tia.ExitError
		if errors.As(err, &ee) {
			return ee.Code
		}
		fmt.Fprintf(stderr, "tia: %v\n", err)
		return exitError
	}
	return exitOK
}

// explain prints the selection rationale.
func explain(w io.Writer, res *tia.PlanResult) {
	sel := res.Selection
	fmt.Fprintf(w, "tia: %d changed ranges, %d seeds, %d affected symbols\n",
		len(res.Ranges), len(res.Seeds.Seeds), len(sel.Affected))
	for i := range res.Seeds.Seeds {
		s := &res.Seeds.Seeds[i]
		fmt.Fprintf(w, "  seed %s (%s: %s)\n", s.Symbol.ID, s.Reason, s.Cause)
	}
	for _, h := range sel.StoppedAtHubs {
		fmt.Fprintf(w, "  stopped at hub %s\n", h)
	}
	if sel.Fallback != "" {
		fmt.Fprintf(w, "  fallback: %s changed, selecting every integration test\n", sel.Fallback)
	}
	for i := range sel.Tests {
		t := &sel.Tests[i]
		fmt.Fprintf(w, "  test %s (%s: %s)\n", t.Symbol.ID, t.Reason, strings.Join(t.Path, " <- "))
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
