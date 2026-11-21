package tia

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestBuildCommand(t *testing.T) {
	sel := &Selection{Tests: []SelectedTest{
		{Symbol: SymbolNode{BaseName: "TestB", PkgDir: "./b"}},
		{Symbol: SymbolNode{BaseName: "TestA", PkgDir: "./a"}},
		{Symbol: SymbolNode{BaseName: "TestA", PkgDir: "./b"}},
		{Symbol: SymbolNode{BaseName: "Test", PkgDir: "."}},
	}}
	got := BuildCommand(sel, CommandOptions{Tag: "db", Parallel: 3})
	want := []string{"test", "-tags=db", "-p", "3", "-count=1", "-run", "^(Test|TestA|TestB)$", ".", "./a", "./b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildCommand() = %q, want %q", got, want)
	}

	got = BuildCommand(&Selection{Tests: sel.Tests[:1]}, CommandOptions{})
	want = []string{"test", "-tags=integration", "-p", "1", "-count=1", "-run", "^(TestB)$", "./b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BuildCommand(defaults) = %q, want %q", got, want)
	}
	if got := BuildCommand(&Selection{}, CommandOptions{}); got != nil {
		t.Errorf("BuildCommand(empty) = %q, want nil", got)
	}
}

func TestFormatCommand(t *testing.T) {
	got := FormatCommand([]string{"test", "-tags=integration", "-run", "^(TestA|TestB)$", "./a", "it's"})
	want := `go test -tags=integration -run '^(TestA|TestB)$' ./a 'it'\''s'`
	if got != want {
		t.Errorf("FormatCommand() = %s, want %s", got, want)
	}
}

func TestExec(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go")
	}
	var out bytes.Buffer
	if err := Exec(context.Background(), t.TempDir(), []string{"version"}, &out, &out); err != nil {
		t.Fatalf("Exec(go version) error = %v", err)
	}
	err := Exec(context.Background(), t.TempDir(), []string{"no-such-subcommand"}, &out, &out)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code == 0 {
		t.Errorf("Exec(bad) error = %v, want *ExitError", err)
	}
}
