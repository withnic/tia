package tia

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSyntheticRepo creates a module with pkgs packages of files files each,
// every file declaring a struct, a constructor, methods and a test.
func writeSyntheticRepo(tb testing.TB, pkgs, files int) string {
	tb.Helper()
	root := tb.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/big\n"), 0o600); err != nil {
		tb.Fatal(err)
	}
	for p := range pkgs {
		dir := filepath.Join(root, "internal", fmt.Sprintf("pkg%03d", p))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			tb.Fatal(err)
		}
		for f := range files {
			var b strings.Builder
			fmt.Fprintf(&b, "package pkg%03d\n\nimport (\n\t\"context\"\n\t\"example.com/big/internal/pkg%03d\"\n)\n\n", p, (p+1)%pkgs)
			for d := range 10 {
				fmt.Fprintf(&b, "// T%d_%d is a type.\ntype T%d_%d struct{ dep *pkg%03d.T0_0 }\n\n", f, d, f, d, (p+1)%pkgs)
				fmt.Fprintf(&b, "func NewT%d_%d(ctx context.Context) *T%d_%d {\n\tif ctx == nil {\n\t\treturn nil\n\t}\n\treturn &T%d_%d{}\n}\n\n", f, d, f, d, f, d)
				fmt.Fprintf(&b, "func (t *T%d_%d) Do(x int) (int, error) {\n\tfor i := range x {\n\t\tx += i\n\t}\n\treturn x, nil\n}\n\n", f, d)
			}
			name := fmt.Sprintf("f%02d.go", f)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
				tb.Fatal(err)
			}
			test := fmt.Sprintf("//go:build integration\n\npackage pkg%03d\n\nimport \"testing\"\n\nfunc TestT%d(t *testing.T) { _ = NewT%d_0(nil) }\n", p, f, f)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d_test.go", f)), []byte(test), 0o600); err != nil {
				tb.Fatal(err)
			}
		}
	}
	return root
}

// BenchmarkScanAndSeed measures indexing a medium-sized module (200
// packages x 10 files x 2 = 4,000 files, ~250k lines) and extracting seeds.
// Requirement (docs/04_guidelines.md §7 Phase 3): well under one second.
func BenchmarkScanAndSeed(b *testing.B) {
	root := writeSyntheticRepo(b, 200, 10)
	ranges := []DiffRange{{FilePath: "internal/pkg042/f03.go", StartLine: 20, EndLine: 24}}
	b.ResetTimer()
	for b.Loop() {
		idx, err := Scan(context.Background(), ScanOptions{Root: root})
		if err != nil {
			b.Fatal(err)
		}
		if s := FindSeeds(idx, ranges); len(s.Seeds) == 0 {
			b.Fatal("no seeds")
		}
	}
}
