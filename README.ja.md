# Muse連携ハブ

[English](README.md) | 日本語

自宅PC上のClaude・Codexから、tailnet内のMuseを通じて使うGo製MCPサーバーです。Streamable HTTPで`/mcp`に接続し、`ping`、`echo`、メモリ内のダミージョブ管理を提供します。個人データを読み書きせず、CLIや外部プロセスも起動しません。

## 必要なもの

- Windows x64
- Go 1.26以降
- Tailscaleクライアント（tailnet内に限定して公開する場合）

## ビルドとテスト

リポジトリのルートでPowerShellを開きます。

```powershell
go test ./...
go build -o muse-hub.exe ./cmd/muse-hub
```

## 設定と起動

`.env.example`は設定項目の見本です。アプリは`.env`を自動読込しないため、PowerShellの環境変数として設定します。トークンはリポジトリ外に保存し、共有・ログ出力しないでください。

```powershell
# 32バイトの暗号学的乱数をBase64 URL-safe形式で生成
$bytes = New-Object byte[] 32
[System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$token = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
$env:MUSE_HUB_TOKEN = $token
$env:MUSE_HUB_AUTH_HEADER = 'Authorization'
$env:MUSE_HUB_AUTH_SCHEME = 'Bearer'
$env:MUSE_HUB_PORT = '8787'

.\muse-hub.exe
```

初回は`go run ./cmd/muse-hub`でも起動できます。トークンが未設定または短すぎる場合、ポート設定が不正な場合、待受を開始できない場合は起動を拒否します。待受先は`http://127.0.0.1:8787/mcp`です。

### 認証方式の設定

既定値は次のとおりです。

| 環境変数 | 既定値 | 説明 |
| --- | --- | --- |
| `MUSE_HUB_TOKEN` | なし（必須） | 32バイト以上の秘密トークン |
| `MUSE_HUB_AUTH_HEADER` | `Authorization` | 認証ヘッダー名 |
| `MUSE_HUB_AUTH_SCHEME` | `Bearer` | `Bearer`の場合は`Bearer <token>`。それ以外は値としてトークンだけを送信 |
| `MUSE_HUB_PORT` | `8787` | ローカル待受ポート（1〜65535） |

Museがカスタムコネクタでどの形式を送れるかは、このサーバーだけでは確定できません。`Authorization: Bearer`が通らない場合は、Muse側で確認した形式に合わせてヘッダー設定を変更してください。

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

これは本人がMuse上で行う確認です。Codexは実行しません。

1. サーバーを起動し、同じPCから`ping`が通ることを確認します。
2. 下記のTailscale Serve手順でtailnet内だけに公開し、表示されたHTTPS URLを記録します。Museには`https://<your-tailnet-host>/mcp`を設定します。実際のtailnet名やホスト名は文書やリポジトリに書かないでください。
3. Museにカスタムコネクタを作ってもらい、URLとトークンを設定します。トークンはMuse側の入力欄へ本人が直接入力します。
4. `ping`と`echo`を呼び、Muse側のSentinel/承認の挙動も確認します。
5. `start_dummy_job`を1秒で開始し、返されたIDを別の呼び出しで`get_result`に渡します。
6. 30〜60秒の待機、別デバイス・別経路からの拒否、認証失敗ログに秘密が含まれないことを確認します。
7. 結果を検証表へ記録してから、Phase 1へ進むか決めます。

### Tailscale Serve

サーバーを起動したまま、別のPowerShellで実行します。Funnelは使わないでください。

```powershell
tailscale serve --bg 8787
tailscale serve status
```

出力されたtailnet内URLに`/mcp`を付けて接続します。公開を止めるときは次を実行します。

```powershell
tailscale serve off
```

`tailscale serve off`はこのPCのServe設定を停止します。実行前に`tailscale serve status`で他のServe設定がないことを確認してください。Tailscale ACL・grants・タグ、OSファイアウォール、他サービスの設定はこのプロジェクトから変更しません。

## ライセンス候補

ライセンスはまだ選んでいません。公開予定のコードで一般的な候補としてMIT LicenseとApache License 2.0があります。MITは短く扱いやすく、Apache 2.0は明示的な特許ライセンス条項を含みます。どちらにするか、または別のライセンスにするかを公開前に決めてください。現時点では`LICENSE`ファイルを置いていません。

## 公開前の確認

- `.env`、トークン、実際のtailnet名・ホスト名を含めない。
- `git status`とコミット差分を確認してから公開する。
- GitHubリポジトリ作成とpushは、内容を本人が確認してから行う。
