package tia

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// DiffOptions configures ChangedRanges.
type DiffOptions struct {
	// Dir is the directory git runs in; paths are reported relative to it
	// (git diff --relative). It is normally the module root.
	Dir string
	// Base is the branch, tag or commit to compare against (e.g. "main").
	// Changes are computed from the merge base of Base and HEAD.
	Base string
	// Dirty includes uncommitted changes: staged, unstaged and untracked
	// files. When false only committed changes (merge base..HEAD) are used.
	Dirty bool
}

// ChangedRanges returns the changed line ranges between the merge base of
// opts.Base and HEAD (and, with opts.Dirty, the working tree), sorted by file
// path and line.
//
// With Dirty set, the merge base is diffed directly against the working tree
// so that every line number refers to the files on disk, which is what the
// scanner parses. Without Dirty, line numbers refer to HEAD; they only match
// the files on disk when the working tree is clean.
func ChangedRanges(ctx context.Context, opts DiffOptions) ([]DiffRange, error) {
	if opts.Base == "" {
		return nil, errors.New("git diff: base revision is empty")
	}
	mb, err := runGit(ctx, opts.Dir, "merge-base", opts.Base, "HEAD")
	if err != nil {
		return nil, err
	}
	base := strings.TrimSpace(string(mb))

	args := []string{
		"-c", "core.quotePath=false",
		"diff", "--unified=0", "--no-renames", "--no-color", "--no-ext-diff", "--relative",
		base,
	}
	if !opts.Dirty {
		args = append(args, "HEAD")
	}
	out, err := runGit(ctx, opts.Dir, args...)
	if err != nil {
		return nil, err
	}
	ranges, err := ParseUnifiedDiff(bytes.NewReader(out))
	if err != nil {
		return nil, err
	}

	if opts.Dirty {
		untracked, err := runGit(ctx, opts.Dir, "-c", "core.quotePath=false",
			"ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, p := range strings.Split(string(untracked), "\x00") {
			if p != "" {
				ranges = append(ranges, DiffRange{FilePath: p})
			}
		}
	}
	sortRanges(ranges)
	return ranges, nil
}

// runGit runs git in dir and returns its standard output.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// ParseUnifiedDiff parses the output of `git diff --unified=0 --no-renames`.
//
// Every hunk becomes one DiffRange over the new (post-image) line numbers. A
// pure deletion (+N,0) is reported as lines N..N+1, the lines surrounding the
// removed text. Deleted files are reported with Deleted set, and files
// without hunks (binary files, mode changes) as whole-file changes.
func ParseUnifiedDiff(r io.Reader) ([]DiffRange, error) {
	var (
		out      []DiffRange
		cur      *diffFile
		lineNo   int
		scanner  = bufio.NewScanner(r)
		finished = func() {
			if cur != nil {
				out = append(out, cur.ranges()...)
			}
		}
	)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			finished()
			cur = &diffFile{header: strings.TrimPrefix(line, "diff --git ")}
		case cur == nil:
			// Preamble before the first file header; ignore.
		case cur.inHunk && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") ||
			strings.HasPrefix(line, " ") || strings.HasPrefix(line, `\`)):
			// Hunk body; line numbers come from the hunk header.
		case strings.HasPrefix(line, "--- "):
			p, err := diffPath(strings.TrimPrefix(line, "--- "), "a/")
			if err != nil {
				return nil, fmt.Errorf("diff line %d: %w", lineNo, err)
			}
			cur.oldPath = p
		case strings.HasPrefix(line, "+++ "):
			p, err := diffPath(strings.TrimPrefix(line, "+++ "), "b/")
			if err != nil {
				return nil, fmt.Errorf("diff line %d: %w", lineNo, err)
			}
			cur.newPath = p
		case strings.HasPrefix(line, "@@ "):
			start, count, err := parseHunkHeader(line)
			if err != nil {
				return nil, fmt.Errorf("diff line %d: %w", lineNo, err)
			}
			cur.inHunk = true
			if count == 0 {
				// Pure deletion after line start: touch both neighbors.
				cur.hunks = append(cur.hunks, DiffRange{StartLine: max(start, 1), EndLine: start + 1})
			} else {
				cur.hunks = append(cur.hunks, DiffRange{StartLine: start, EndLine: start + count - 1})
			}
		case strings.HasPrefix(line, "deleted file mode"):
			cur.deleted = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading diff: %w", err)
	}
	finished()
	sortRanges(out)
	return out, nil
}

// diffFile accumulates one file section of a diff.
type diffFile struct {
	header           string // "a/x b/x" from the diff --git line
	oldPath, newPath string // "" for /dev/null or when absent
	deleted, inHunk  bool
	hunks            []DiffRange
}

func (f *diffFile) path() string {
	switch {
	case f.newPath != "":
		return f.newPath
	case f.oldPath != "":
		return f.oldPath
	}
	// No ---/+++ lines (binary file or mode change): fall back to the
	// header. Without renames both sides name the same file, so the header
	// is "a/<p> b/<p>" and <p> is its second half.
	h := f.header
	if n := len(h); n >= 5 && (n-1)%2 == 0 {
		half := (n - 1) / 2
		if a, b := h[:half], h[half+1:]; strings.HasPrefix(a, "a/") && strings.HasPrefix(b, "b/") && a[2:] == b[2:] {
			return b[2:]
		}
	}
	if p, err := diffPath(h, "a/"); err == nil {
		if a, _, ok := strings.Cut(p, " b/"); ok {
			return a
		}
		return p
	}
	return h
}

func (f *diffFile) ranges() []DiffRange {
	p := f.path()
	if f.deleted {
		return []DiffRange{{FilePath: p, Deleted: true}}
	}
	if len(f.hunks) == 0 {
		return []DiffRange{{FilePath: p}}
	}
	out := make([]DiffRange, len(f.hunks))
	for i, h := range f.hunks {
		h.FilePath = p
		out[i] = h
	}
	return out
}

// diffPath decodes a path from a ---/+++ line: it strips the a/ or b/
// prefix, unquotes C-style quoted names and maps /dev/null to "".
func diffPath(s, prefix string) (string, error) {
	s = strings.TrimSuffix(s, "\t")
	if s == "/dev/null" {
		return "", nil
	}
	if strings.HasPrefix(s, `"`) {
		u, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("unquoting path %s: %w", s, err)
		}
		s = u
	}
	return strings.TrimPrefix(s, prefix), nil
}

// parseHunkHeader extracts the post-image start line and line count from
// "@@ -a[,b] +c[,d] @@ ...".
func parseHunkHeader(line string) (start, count int, err error) {
	fields := strings.Fields(line)
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, fmt.Errorf("malformed hunk header %q", line)
	}
	spec := strings.TrimPrefix(fields[2], "+")
	startStr, countStr, hasCount := strings.Cut(spec, ",")
	if start, err = strconv.Atoi(startStr); err != nil {
		return 0, 0, fmt.Errorf("malformed hunk header %q: %w", line, err)
	}
	count = 1
	if hasCount {
		if count, err = strconv.Atoi(countStr); err != nil {
			return 0, 0, fmt.Errorf("malformed hunk header %q: %w", line, err)
		}
	}
	return start, count, nil
}

func sortRanges(rs []DiffRange) {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].FilePath != rs[j].FilePath {
			return rs[i].FilePath < rs[j].FilePath
		}
		if rs[i].StartLine != rs[j].StartLine {
			return rs[i].StartLine < rs[j].StartLine
		}
		return rs[i].EndLine < rs[j].EndLine
	})
}
