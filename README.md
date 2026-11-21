# tia

[![CI](https://github.com/withnic/tia/actions/workflows/ci.yml/badge.svg)](https://github.com/withnic/tia/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/withnic/tia)](https://goreportcard.com/report/github.com/withnic/tia)

Test Impact Analysis for Go — select and run only the integration tests affected by your changes.

[日本語](README_ja.md)

## Overview

`tia` analyzes `git diff` to find changed functions, variables, structs, and interfaces, then traces AST references in reverse to identify which integration tests are affected. Hub functions (like `NewServer`) are detected and stopped to prevent over-selection.

## Install

```
go install github.com/withnic/tia/cmd/tia@latest
```

## Usage

```
tia [flags]
```

Run it inside a Go module on a feature branch. It prints a `go test` command for the affected integration tests:

```
$ tia -base main -hub-exclude 'NewServer,Initialize*'
go test -tags=integration -p 1 -count=1 -run '^(TestRepoCreate|TestServiceCreate)$' ./internal/repo ./internal/service
```

Add `-exec` to run the tests directly, or `-v` to see why each test was selected.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-base` | `main` | Base branch to compare against |
| `-tag` | `integration` | Build tag of integration test files |
| `-pattern` | `_integration_test.go` | File name suffix of integration test files |
| `-dirty` | `true` | Include uncommitted and untracked changes |
| `-exec` | `false` | Run the selected tests instead of printing the command |
| `-parallel-pkg` | `1` | Packages tested in parallel (`go test -p`) |
| `-hub-exclude` | | Comma-separated hub function name patterns where traversal stops |
| `-v` | `false` | Explain why each test was selected |

## How it works

1. **Diff** — extract changed files and line ranges from `git diff`
2. **Scan** — parallel AST scan of the entire module (no type checking, fast)
3. **Seed** — map changed lines to top-level declarations (seeds)
4. **Analyze** — reverse BFS from seeds through references, stopping at hub functions
5. **Select** — collect `TestXxx` functions in `_integration_test.go` files reached by the traversal

## License

[MIT](LICENSE)
