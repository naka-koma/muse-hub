# Muse Integration Hub

English | [日本語](README.ja.md)

A Go MCP server that lets Muse delegate jobs to Claude and Codex on a home PC.

## Setup

Use Windows x64 with Go 1.26 or later and PowerShell 7. Build from the repository root.

```powershell
go build -o muse-hub.exe ./cmd/muse-hub
```

## First run

Generate a token and start the server in the same PowerShell session.

```powershell
$bytes = New-Object byte[] 32
[System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$env:MUSE_HUB_TOKEN = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
.\muse-hub.exe
```

The server listens at `http://127.0.0.1:8787/mcp`. See the [usage guide](docs/usage.md) for Muse connectivity and authentication settings.

[MIT License](LICENSE)
