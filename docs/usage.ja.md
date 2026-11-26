# Muse連携ハブの使い方

[English](usage.md) | 日本語 | [セットアップと初回実行](../README.ja.md)

## 環境変数

[.env.example](../.env.example) に設定項目をまとめています。アプリは `.env` を自動で読み込まないため、起動するPowerShellで環境変数を設定してください。トークンはリポジトリの外で管理してください。

| 環境変数 | 既定値 | 説明 |
| --- | --- | --- |
| `MUSE_HUB_TOKEN` | なし（必須） | 32バイト以上の秘密トークン |
| `MUSE_HUB_AUTH_HEADER` | `Authorization` | 認証ヘッダー名 |
| `MUSE_HUB_AUTH_SCHEME` | `Bearer` | `Bearer`の場合は`Bearer <token>`。それ以外は値としてトークンだけを送信 |
| `MUSE_HUB_PORT` | `8787` | ローカル待受ポート（1〜65535） |
| `MUSE_HUB_TAILSCALE_HOST` | 未設定 | Serve経由の接続を許可するTailscaleのホスト名。HTTPSポートが443以外なら`:ポート番号`を付けます。 |
| `MUSE_HUB_PHASE1_AUTH_VOLUME` | 未設定 | 専用のDockerログインボリューム。設定すると [Phase 1の `ask` 検証](phase1-isolation.ja.md) を有効にします。 |

Museがカスタムコネクタでどの形式を送れるかは、このサーバーだけでは確定できません。`Authorization: Bearer`が通らない場合は、Muse側で確認した形式に合わせてヘッダー設定を変更してください。

実行ファイルを作らずに起動するには、トークンを設定し、リポジトリのルートで `go run ./cmd/muse-hub` を実行します。起動に失敗した場合はトークンの長さとポートの設定を確認し、同じポートを別のプロセスが使っていないか調べてください。

## セキュリティとデータの扱い

- サーバーは`127.0.0.1`だけで待ち受けます。tailnet内から接続するにはTailscale Serveを使います。
- ログにはツール名、許可・拒否、job ID、処理時間を記録します。トークン、ヘッダー、`echo`の入力は記録しません。
- 個人データを読み書きせず、CLIや外部プロセスも起動しません。

## ツール

| ツール | 入力 | 成功時の戻り値 |
| --- | --- | --- |
| `ping` | なし | `status: "ok"`とUTC時刻（ISO 8601） |
| `echo` | `text`（最大1,000文字） | 同じ`text`。上限超過は入力エラー |
| `start_dummy_job` | `delay_seconds`（整数1〜60） | 推測困難な128ビット`job_id` |
| `get_result` | `job_id` | `queued` / `running` / `succeeded`。完了時のみ固定結果と開始・完了時刻 |

存在しないIDは`job_not_found`を返し、スタックトレースは公開しません。ジョブの状態と結果はプロセスのメモリ内にだけ保存され、再起動で消えます。
同時に実行できるジョブは最大5件です。完了結果は1時間保持します。

## 手元から疎通確認

別のPowerShellウィンドウでトークンを入力し、MCPを初期化して`ping`を呼び出します。

```powershell
$token = Read-Host 'MUSE_HUB_TOKEN'
$headers = @{
  Authorization = "Bearer $token"
  Accept = 'application/json, text/event-stream'
}
$init = '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl-check","version":"1.0"}}}'
$initialized = Invoke-WebRequest -Method Post -Uri 'http://127.0.0.1:8787/mcp' -Headers $headers -ContentType 'application/json' -Body $init
$sessionId = [string]$initialized.Headers['Mcp-Session-Id']
$headers['Mcp-Session-Id'] = $sessionId
$headers['MCP-Protocol-Version'] = '2025-11-25'
$notification = '{"jsonrpc":"2.0","method":"notifications/initialized"}'
Invoke-WebRequest -Method Post -Uri 'http://127.0.0.1:8787/mcp' -Headers $headers -ContentType 'application/json' -Body $notification | Out-Null
$ping = '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping","arguments":{}}}'
Invoke-WebRequest -Method Post -Uri 'http://127.0.0.1:8787/mcp' -Headers $headers -ContentType 'application/json' -Body $ping
```

PowerShellの`Invoke-WebRequest`がレスポンスをイベントストリームとして扱う環境では、MCP対応クライアントを使ってください。`go test ./...`でも疎通を確認できます。ローカルHTTPの確認にはBearerトークンを使います。トークンを第三者に渡さないでください。

## Museから試す手順

サーバーを動かすPCでTailscaleクライアントを起動し、tailnetに参加していることを確認します。

1. サーバーを起動し、同じPCから`ping`が通ることを確認します。
2. 下記のTailscale Serve手順に従います。表示されたHTTPS URLの末尾に `/mcp` を付けます。
3. Museにカスタムコネクタを作ってもらい、URLとトークンを設定します。トークンはMuse側の入力欄へ本人が直接入力します。
4. `ping`と`echo`を呼び、Muse側のSentinel/承認の挙動も確認します。
5. `start_dummy_job`を1秒で開始し、返されたIDを別の呼び出しで`get_result`に渡します。
6. 30〜60秒の待機、別デバイス・別経路からの拒否、認証失敗ログに秘密が含まれないことを確認します。

### Tailscale Serve

サーバーを起動するPowerShellで、`MUSE_HUB_TAILSCALE_HOST` にServeのURLのホスト名を設定してください。`https://` と `/mcp` は省き、HTTPSポートが443以外なら `:ポート番号` を付けます。443を使う場合は、次のコマンドで設定できます。

```powershell
$env:MUSE_HUB_TAILSCALE_HOST = (tailscale status --json | ConvertFrom-Json).Self.DNSName.TrimEnd('.')
```

サーバーを起動または再起動した後、別のPowerShellで次のコマンドを実行します。`MUSE_HUB_PORT` を変更した場合は `8787` をそのポート番号に置き換えてください。

```powershell
tailscale serve 8787
```

別のServe設定が443を使っている場合は、たとえば `tailscale serve --https=9443 8787` で空いているHTTPSポートを指定します。`MUSE_HUB_TAILSCALE_HOST` にも同じHTTPSポートを付けてください。検証中はServeを起動したままにします。停止するには、Serveを起動したPowerShellで `Ctrl+C` を押します。

## テスト

リポジトリのルートで実行します。

```powershell
go test ./...
```
