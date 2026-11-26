# Phase 1の隔離検証

[English](phase1-isolation.md) | 日本語

Phase 1では、Dockerで隔離したClaude Codeがダミーの質問に回答できるかを確認します。Claudeには本人のアカウントでログインし、認証情報は専用のDockerボリュームに保存します。コンテナを単体で確認してから、Hubの `ask` を有効にできます。

## 隔離の範囲

- CLIは非rootユーザーで実行します。コンテナのファイルシステムは読み取り専用で、書き込めるのは専用のホームボリュームと一時ディレクトリです。ホストのフォルダ、Dockerソケット、Hubの設定、APIキーは渡しません。
- 質問を送るときは、組み込みツール、MCPサーバー、Claudeアカウントのコネクタ、フック、スキル、セッション保存を無効にします。ログイン情報は専用ボリュームに残ります。
- CLIはDockerの内部ネットワークに接続します。別コンテナのプロキシは、指定したAnthropicのホスト宛てに443番ポートのHTTPSトンネルを許可します。プロキシのポートは公開しません。判定するのはCONNECTの宛先で、TLS内のHTTPパスは検査しません。

## 検証手順

Docker DesktopでLinuxコンテナとDocker Composeを使います。リポジトリのルートでPowerShell 7を開きます。

```powershell
$phase1 = @('-p', 'muse-phase1-check', '-f', 'experiments/phase1/compose.yaml')
docker volume create muse-phase1-claude-auth
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

## Hubで `ask` を有効にする

Composeのプロキシを起動したままにします。Hubを起動するPowerShellで、[README](../README.ja.md) の手順どおりにトークンを設定します。続いてログイン用のボリューム名を設定し、サーバーを起動します。

```powershell
$env:MUSE_HUB_PHASE1_AUTH_VOLUME = 'muse-phase1-claude-auth'
go run ./cmd/muse-hub
```

Hubは待ち受けを始める前に、コンテナの隔離とClaudeのログインを確認します。起動に失敗した場合は隔離検証を再実行し、`proxy` サービスが起動しているか確認してください。既定のツールだけを使う場合は、ボリューム名を設定せずに起動します。

Museで試す場合は、[Tailscale Serveの手順](usage.ja.md#tailscale-serve) に従って接続し、次の引数で `ask` を呼び出します。

```json
{"agent":"claude","task":"2 + 3","context":"","data_category":["code"]}
```

期待する結果は `{"answer":"5"}` です。Phase 1で受け付けるのはこのダミーの質問だけです。Hubは固定の質問文をClaudeに送ります。

検証が終わったらHubを停止し、`docker compose @phase1 down` を実行します。ログイン情報も削除する場合は、コンテナの停止後に `docker volume rm muse-phase1-claude-auth` を実行してください。
