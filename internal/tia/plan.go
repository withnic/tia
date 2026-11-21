package tia

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PlanOptions configures Plan.
type PlanOptions struct {
	// Root is the module root (directory containing go.mod).
	Root string
	// Base is the revision to compare against, e.g. "main".
	Base string
	// Dirty includes uncommitted and untracked changes.
	Dirty bool
	// Tag is the integration build tag (default "integration").
	Tag string
	// Pattern is the integration test file suffix (default
	// "_integration_test.go").
	Pattern string
	// HubPatterns are path.Match patterns for hub function names.
	HubPatterns []string
	// Parallel is the -p value of the generated command.
	Parallel int
}

// PlanResult is the outcome of Plan.
type PlanResult struct {
	Ranges    []DiffRange
	Seeds     *SeedSet
	Selection *Selection
	// Command holds the go arguments to run (nil when no test is affected).
	Command []string
	// Warnings are non-fatal issues worth telling the user about.
	Warnings []string
}

// Plan runs the whole TIA pipeline: git diff -> scan -> seeds -> reverse
// traversal -> go test command.
func Plan(ctx context.Context, opts PlanOptions) (*PlanResult, error) {
	res := &PlanResult{}
	if !opts.Dirty {
		out, err := runGit(ctx, opts.Root, "status", "--porcelain", "--untracked-files=no", ".")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(out)) != "" {
			res.Warnings = append(res.Warnings,
				"the working tree has uncommitted changes that are ignored (-dirty=false); line numbers may not match the files on disk")
		}
	}

	ranges, err := ChangedRanges(ctx, DiffOptions{Dir: opts.Root, Base: opts.Base, Dirty: opts.Dirty})
	if err != nil {
		return nil, err
	}
	idx, err := Scan(ctx, ScanOptions{Root: opts.Root, Tag: opts.Tag, Pattern: opts.Pattern, HubPatterns: opts.HubPatterns})
	if err != nil {
		return nil, err
	}

	for _, f := range idx.Files {
		if f.ParseError != "" {
			res.Warnings = append(res.Warnings, "cannot parse "+f.Path+": "+f.ParseError)
		}
	}

	res.Ranges = ranges
	res.Seeds = FindSeeds(idx, ranges)
	res.Selection = Analyze(idx, res.Seeds)
	res.Command = BuildCommand(res.Selection, CommandOptions{Tag: opts.Tag, Parallel: opts.Parallel})
	return res, nil
}

// FindModuleRoot returns the nearest directory at or above dir containing a
// go.mod file.
func FindModuleRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("%w: no go.mod in %s or any parent directory", ErrNoModule, dir)
		}
		d = parent
	}
}
