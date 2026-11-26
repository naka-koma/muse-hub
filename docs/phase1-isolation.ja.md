# Phase 1の隔離検証

[English](phase1-isolation.md) | 日本語

Dockerで隔離したClaude Codeが、ダミーの質問に回答できるかを確認します。Claudeへのログインには本人のアカウントを使い、認証情報は専用のDockerボリュームに保存します。この検証の対象はClaudeの実行環境です。

## 隔離の範囲

- CLIは非rootユーザーで実行します。コンテナのファイルシステムは読み取り専用で、書き込めるのは専用のホームボリュームと一時ディレクトリです。ホストのフォルダ、Dockerソケット、Hubの設定、APIキーは渡しません。
- 質問を送るときは、組み込みツール、MCPサーバー、Claudeアカウントのコネクタ、フック、スキル、セッション保存を無効にします。ログイン情報は専用ボリュームに残ります。
- CLIはDockerの内部ネットワークに接続します。別コンテナのプロキシは、指定したAnthropicのホスト宛てに443番ポートのHTTPSトンネルを許可します。プロキシのポートは公開しません。判定するのはCONNECTの宛先で、TLS内のHTTPパスは検査しません。

## 検証手順

Docker DesktopでLinuxコンテナとDocker Composeを使います。リポジトリのルートでPowerShell 7を開きます。

```powershell
$phase1 = @('-p', 'muse-phase1-check', '-f', 'experiments/phase1/compose.yaml')
docker compose @phase1 build
docker compose @phase1 run --rm -T --entrypoint node claude /opt/verify.mjs
```

ファイルと認証情報の境界、デフォルト経路がないこと、許可外のホストとポートの拒否、許可した2つのホストへのTLS接続を確認します。このコマンドはモデルを呼び出しません。

コンテナ内でログインします。CLIの案内に従ってブラウザーで認証してください。ログイン情報はこの検証専用のDockerボリュームに保存されます。

```powershell
docker compose @phase1 run --rm claude auth login --claudeai
```

ダミーの質問を送ります。期待する回答は `5` です。

```powershell
$phase1Settings = '{"disableAllHooks":true,"disableClaudeAiConnectors":true}'
docker compose @phase1 run --rm -T claude --print 'What is 2 + 3? Reply with only the digit.' `
  --tools '' --strict-mcp-config --setting-sources '' --disable-slash-commands `
  --settings $phase1Settings --no-session-persistence --max-turns 1
```

停止するには `docker compose @phase1 down` を実行します。ログイン情報も削除する場合は `docker compose @phase1 down --volumes` を使ってください。
