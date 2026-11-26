# Muse連携ハブ

[English](README.md) | 日本語

Museから自宅PCへの接続を試すためのGo製MCPサーバーです。`ping`、`echo`、ダミージョブを使って疎通を確認できます。

## セットアップ

Windows x64で、Go 1.26以降とPowerShell 7を使います。リポジトリのルートでビルドします。

```powershell
go build -o muse-hub.exe ./cmd/muse-hub
```

## 初回実行

同じPowerShellでトークンを生成して起動します。

```powershell
$bytes = New-Object byte[] 32
[System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$env:MUSE_HUB_TOKEN = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
.\muse-hub.exe
```

起動後は `http://127.0.0.1:8787/mcp` に接続できます。Museから接続する手順や認証の設定は[利用ガイド](docs/usage.ja.md)を参照してください。

[MITライセンス](LICENSE)
