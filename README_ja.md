# tia

[![CI](https://github.com/withnic/tia/actions/workflows/ci.yml/badge.svg)](https://github.com/withnic/tia/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/withnic/tia)](https://goreportcard.com/report/github.com/withnic/tia)

Go 向け Test Impact Analysis — 変更の影響を受ける結合テストだけを選択・実行します。

## 概要

`tia` は `git diff` を解析し、変更された関数・変数・構造体・インターフェースを起点に AST の参照関係を逆引きして、影響を受ける結合テストを特定します。ハブ関数（`NewServer` 等）を検出して探索を打ち止めることで、過剰なテスト選択を防ぎます。

## インストール

```
go install github.com/withnic/tia/cmd/tia@latest
```

## 使い方

```
tia [flags]
```

Go モジュール内のフィーチャーブランチで実行すると、影響を受ける結合テストの `go test` コマンドを出力します:

```
$ tia -base main -hub-exclude 'NewServer,Initialize*'
go test -tags=integration -p 1 -count=1 -run '^(TestRepoCreate|TestServiceCreate)$' ./internal/repo ./internal/service
```

`-exec` でそのまま実行、`-v` で各テストの選択理由を表示します。

### フラグ

| フラグ | デフォルト | 説明 |
|--------|-----------|------|
| `-base` | `main` | 比較対象のベースブランチ |
| `-tag` | `integration` | 結合テストのビルドタグ |
| `-pattern` | `_integration_test.go` | 結合テストのファイル名サフィックス |
| `-dirty` | `true` | 未コミットの変更を含めるか |
| `-exec` | `false` | テストを出力せず直接実行する |
| `-parallel-pkg` | `1` | パッケージ並列数（`go test -p`） |
| `-hub-exclude` | | 探索を打ち止めるハブ関数名パターン（カンマ区切り） |
| `-v` | `false` | 各テストの選択理由を表示 |

## 仕組み

1. **差分抽出** — `git diff` から変更ファイルと行範囲を取得
2. **AST スキャン** — モジュール全体を並列で軽量パース（型チェックなし、高速）
3. **シード特定** — 変更行をトップレベル宣言（シード）にマッピング
4. **逆引き解析** — シードから参照関係を逆方向に BFS 探索、ハブ関数で停止
5. **テスト選択** — 探索で到達した `_integration_test.go` 内の `TestXxx` 関数を収集

## ライセンス

[MIT](LICENSE)
