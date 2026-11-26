# Muse Integration Hub

English | [日本語](README.ja.md)

This is a Go MCP server that Claude and Codex on a home PC can use through Muse in the tailnet. It uses Streamable HTTP at `/mcp` and provides `ping`, `echo`, and in-memory dummy jobs. It does not read or write personal data or launch CLIs or other external processes.

## Requirements

- Windows x64
- Go 1.26 or later
- Tailscale client, if you plan to expose the server only within the tailnet

## Build and test

Open PowerShell in the repository root and run:

```powershell
go test ./...
go build -o muse-hub.exe ./cmd/muse-hub
```

## Configure and start

`.env.example` lists the available settings. The program does not load `.env` automatically, so set the values as PowerShell environment variables. Keep the token outside the repository. Do not share it or print it in logs.

```powershell
# Generate 32 bytes of cryptographically secure randomness in Base64 URL-safe form
$bytes = New-Object byte[] 32
[System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
$token = [Convert]::ToBase64String($bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_')
$env:MUSE_HUB_TOKEN = $token
$env:MUSE_HUB_AUTH_HEADER = 'Authorization'
$env:MUSE_HUB_AUTH_SCHEME = 'Bearer'
$env:MUSE_HUB_PORT = '8787'

.\muse-hub.exe
```

For the first run, you can also use `go run ./cmd/muse-hub`. Startup is rejected if the token is missing or too short, the port setting is invalid, or the server cannot listen on the configured address. The server listens at `http://127.0.0.1:8787/mcp` by default.

### Authentication settings

| Environment variable | Default | Description |
| --- | --- | --- |
| `MUSE_HUB_TOKEN` | None; required | Secret token of at least 32 bytes |
| `MUSE_HUB_AUTH_HEADER` | `Authorization` | Authentication header name |
| `MUSE_HUB_AUTH_SCHEME` | `Bearer` | For `Bearer`, send `Bearer <token>`. For any other value, send the token as the header value without a scheme. |
| `MUSE_HUB_PORT` | `8787` | Local listening port, from 1 to 65535 |
| `MUSE_HUB_TAILSCALE_HOST` | Unset | Exact Tailscale hostname to allow for Serve, with `:port` if the HTTPS port is not 443 |

This server alone cannot determine which authentication format Muse's custom connector supports. If `Authorization: Bearer` does not work, confirm the format Muse supports and adjust the header settings accordingly.

## Security and data handling

- The server listens only on `127.0.0.1`. Use Tailscale Serve for access within the tailnet.
- Logs include the tool name, allow or deny outcome, job ID, and duration. They exclude tokens, headers, and `echo` input.
- The server does not read or write personal data, launch a CLI, or start external processes.

## Tools

| Tool | Input | Successful response |
| --- | --- | --- |
| `ping` | None | `status: "ok"` and the current UTC time in ISO 8601 format |
| `echo` | `text`, up to 1,000 characters | The same `text`. Longer input is rejected. |
| `start_dummy_job` | `delay_seconds`, an integer from 1 to 60 | An unpredictable 128-bit `job_id` |
| `get_result` | `job_id` | `queued`, `running`, or `succeeded`; the fixed result and start/completion times are included only when complete |

An unknown ID returns `job_not_found` without a stack trace. Job state and results stay in memory and are lost when the process restarts.
At most five jobs can run at once. Completed results are retained for one hour.

## Check connectivity from this PC

In a separate PowerShell window, enter the token and initialize MCP before calling `ping`:

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

If your PowerShell environment handles the response as an event stream, use an MCP client instead. The integration tests also check connectivity with `go test ./...`. The local HTTP check uses a Bearer token. Do not share the token with anyone else.

## Test from Muse

The following checks must be performed by the user in Muse. Codex does not perform them.

1. Start the server and confirm that `ping` works from the same PC.
2. Use the Tailscale Serve steps below to expose the service only within the tailnet. Record the HTTPS URL that Tailscale displays and configure Muse with `https://<your-tailnet-host>/mcp`. Do not put the actual tailnet or host name in documents or the repository.
3. Ask Muse to create a custom connector and configure the URL and token. Enter the token directly in Muse yourself.
4. Call `ping` and `echo`. Check how Muse handles Sentinel and approval prompts.
5. Start a one-second job with `start_dummy_job`, then pass its returned ID to `get_result` in a separate call.
6. Check a 30-to-60-second wait, rejection from another device or route, and that authentication failure logs contain no secrets.
7. Record the results in the verification table before deciding whether to proceed to Phase 1.

### Tailscale Serve

In the PowerShell window used to start the server, set `MUSE_HUB_TAILSCALE_HOST` to the hostname in the Serve URL. Omit `https://` and `/mcp`. Add `:port` for a custom HTTPS port. The following command sets the hostname for port 443:

```powershell
$env:MUSE_HUB_TAILSCALE_HOST = (tailscale status --json | ConvertFrom-Json).Self.DNSName.TrimEnd('.')
```

Start or restart the server, then execute these commands in another PowerShell window. Do not use Funnel.

```powershell
tailscale serve --bg 8787
tailscale serve status
```

Connect to the tailnet URL shown by the command, with `/mcp` appended. To stop serving, run:

```powershell
tailscale serve off
```

`tailscale serve off` stops the Serve configuration on this PC. Before running it, use `tailscale serve status` to confirm that no other Serve configuration is in use. This project does not change Tailscale ACLs, grants, tags, the OS firewall, or settings for other services.

## License options

No license has been selected. MIT License and Apache License 2.0 are common options for code intended for publication. MIT is short and straightforward. Apache 2.0 includes explicit patent license terms. Choose one of these or another license before publication. There is no `LICENSE` file yet.

## Before publication

- Do not include `.env`, tokens, or the actual tailnet or host name.
- Review `git status` and the commit diff before publishing.
- Create a GitHub repository and push only after reviewing the contents.
