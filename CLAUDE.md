CLAUDE.md
このファイルは、Claude Code が本リポジトリ（tia）で開発・設計を行う際の動作指針とルールを定義します。
1. プロジェクト概要
tia は Go 製の CLI ツールで、Test Impact Analysis 機能を提供します。

git diff で変更された関数・変数・定数・構造体・インターフェースを起点にASTの参照関係を逆引きし、ハブ関数（NewServer 等）による過剰波及を防ぎつつ、影響を受ける結合テスト（//go:build integration / *_integration_test.go）のみを抽出・実行します。
2. ビルド・テストコマンド
開発操作はすべて Makefile 経由で実行する（`make help` で一覧表示）。ツール類（golangci-lint, govulncheck）はバージョン固定で ./bin に自動インストールされる。

全ユニットテスト実行（高速）
make test          # go test -short ./...
結合テスト含む全テスト実行
make test-all      # go test ./...
フォーマット / リント
make fmt           # gofumpt + goimports
make lint          # golangci-lint（.golangci.yml）
CI 相当の一括チェック（tidy・フォーマット・リント・ユニットテスト）
make check
脆弱性スキャン / カバレッジ
make vuln
make cover
バイナリビルド
make build         # bin/tia
3. 開発・実装ルール
検証の徹底
コードを変更したら必ず make check を実行し、フォーマット・リント・ユニットテストがすべてパスすることを確認してから完了報告を行うこと。
ワークスペース管理
タスク進行メモや設計検討の作業ファイルはメインリポジトリにコミットせず、外部ワークスペース（.ai-workspace/）で管理すること。
