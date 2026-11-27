# Claudeの隔離環境を準備する

[English](claude-isolation.md) | 日本語

Claude CodeはDockerコンテナ内で実行します。自分のClaudeアカウントでログインしてください。認証情報は専用のDockerボリュームに保存します。

## 隔離の範囲

- CLIは非rootユーザーで実行します。コンテナのファイルシステムは読み取り専用で、書き込めるのは専用のホームボリュームと一時ディレクトリです。ホストのフォルダ、Dockerソケット、Hubの設定、APIキーは渡しません。
- 質問を送るときは、組み込みツール、MCPサーバー、Claudeアカウントのコネクタ、フック、スキル、セッション保存を無効にします。ログイン情報は専用ボリュームに残ります。
- CLIはDockerの内部ネットワークに接続します。別コンテナのプロキシは、指定したAnthropicのホスト宛てに443番ポートのHTTPSトンネルを許可します。プロキシのポートは公開しません。判定するのはCONNECTの宛先で、TLS内のHTTPパスは検査しません。

## ビルドとログイン

Docker DesktopでLinuxコンテナとDocker Composeを使います。リポジトリのルートでPowerShell 7を開きます。

```powershell
$claudeCompose = @('-f', 'deploy/claude/compose.yaml')
$env:MUSE_HUB_CLAUDE_AUTH_VOLUME = 'muse-hub-claude-auth'
docker volume create $env:MUSE_HUB_CLAUDE_AUTH_VOLUME
docker compose @claudeCompose build
docker compose @claudeCompose up -d proxy
docker compose @claudeCompose run --rm -T --entrypoint node claude /opt/verify.mjs
```

ファイルと認証情報の境界、デフォルト経路がないこと、許可外のホストとポートの拒否、許可した2つのホストへのTLS接続を確認します。このコマンドはモデルを呼び出しません。

コンテナ内でログインします。CLIの案内に従ってブラウザーで認証してください。ログイン情報はこの検証専用のDockerボリュームに保存されます。

```powershell
docker compose @claudeCompose run --rm claude auth login --claudeai
```

ダミーの質問を送ります。期待する回答は `5` です。

```powershell
$claudeSettings = '{"disableAllHooks":true,"disableClaudeAiConnectors":true}'
docker compose @claudeCompose run --rm -T claude --print 'What is 2 + 3? Reply with only the digit.' `
  --tools '' --strict-mcp-config --setting-sources '' --disable-slash-commands `
  --settings $claudeSettings --no-session-persistence --max-turns 1
```

## Hubに接続する

Composeのプロキシを起動したまま、[Claudeへの委譲](jobs.ja.md#claudeに委譲する) を設定します。

### `ask` で同期応答を確認する

Composeのプロキシを起動したままにします。Hubを起動するPowerShellで、[README](../README.ja.md) の手順どおりにトークンを設定します。続いてログイン用のボリューム名を設定し、サーバーを起動します。

```powershell
$env:MUSE_HUB_CLAUDE_ASK_VOLUME = $env:MUSE_HUB_CLAUDE_AUTH_VOLUME
Remove-Item Env:MUSE_HUB_CLAUDE_AUTH_VOLUME
go run ./cmd/muse-hub
```

Hubは待ち受けを始める前に、コンテナの隔離とClaudeのログインを確認します。起動に失敗した場合は隔離検証を再実行し、`proxy` サービスが起動しているか確認してください。既定のツールだけを使う場合は、ボリューム名を設定せずに起動します。

Museで試す場合は、[Tailscale Serveの手順](usage.ja.md#tailscale-serve) に従って接続し、次の引数で `ask` を呼び出します。

```json
{"agent":"claude","task":"2 + 3","context":"","data_category":["code"]}
```

`ask` は固定の質問だけを受け付けます。期待する応答は `{"answer":"5"}` です。

Claudeのワーカーを止めるには、Hubを停止して `docker compose @claudeCompose down` を実行します。ログイン情報を削除する場合は、コンテナを停止してから専用のボリュームを削除します。
