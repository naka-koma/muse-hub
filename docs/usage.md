# Muse Integration Hub usage guide

English | [日本語](usage.ja.md) | [Setup and first run](../README.md)

## Environment variables

[.env.example](../.env.example) lists the available settings. Set them in the PowerShell session that starts the server. The app does not load `.env` automatically. Keep tokens outside the repository.

| Environment variable | Default | Description |
| --- | --- | --- |
| `MUSE_HUB_TOKEN` | None; required | Secret token of at least 32 bytes |
| `MUSE_HUB_AUTH_HEADER` | `Authorization` | Authentication header name |
| `MUSE_HUB_AUTH_SCHEME` | `Bearer` | For `Bearer`, send `Bearer <token>`. For any other value, send the token as the header value without a scheme. |
| `MUSE_HUB_PORT` | `8787` | Local listening port, from 1 to 65535 |
| `MUSE_HUB_TAILSCALE_HOST` | Unset | Exact Tailscale hostname to allow for Serve, with `:port` if the HTTPS port is not 443 |
| `MUSE_HUB_PHASE1_AUTH_VOLUME` | Unset | Dedicated Docker login volume. Setting it enables the [Phase 1 `ask` check](phase1-isolation.md). |
| `MUSE_HUB_PHASE2_DB` | Unset | Local SQLite path. Setting it enables the [Phase 2 asynchronous job check](phase2-jobs.md). |
| `MUSE_HUB_PHASE3_POLICY` | Unset | Local YAML path for [Phase 3 policy and audit](phase3-policy.md). Requires the job database. |
| `MUSE_HUB_PHASE5_APPROVALS` | Unset | Set to `1` to enable [policy proposals and local approval](phase5-approval.md). Requires the policy path and job database. |

This server alone cannot determine which authentication format Muse's custom connector supports. If `Authorization: Bearer` does not work, confirm the format Muse supports and adjust the header settings accordingly.

To start without building an executable first, set the token and run `go run ./cmd/muse-hub` from the repository root. If startup fails, check the token length, the configured port, and whether another process is listening on that port.

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

Start the Tailscale client on the server PC and confirm that it has joined the tailnet.

1. Start the server and confirm that `ping` works from the same PC.
2. Follow the Tailscale Serve steps below. Append `/mcp` to the HTTPS URL that Tailscale displays.
3. Ask Muse to create a custom connector and configure the URL and token. Enter the token directly in Muse yourself.
4. Call `ping` and `echo`. Check how Muse handles Sentinel and approval prompts.
5. Start a one-second job with `start_dummy_job`, then pass its returned ID to `get_result` in a separate call.
6. Check a 30-to-60-second wait, rejection from another device or route, and that authentication failure logs contain no secrets.

### Tailscale Serve

In the PowerShell window used to start the server, set `MUSE_HUB_TAILSCALE_HOST` to the hostname in the Serve URL. Omit `https://` and `/mcp`. Add `:port` for a custom HTTPS port. The following command sets the hostname for port 443:

```powershell
$env:MUSE_HUB_TAILSCALE_HOST = (tailscale status --json | ConvertFrom-Json).Self.DNSName.TrimEnd('.')
```

Start or restart the server, then run the following command in another PowerShell window. If you changed `MUSE_HUB_PORT`, use that port in place of `8787`.

```powershell
tailscale serve 8787
```

If another Serve endpoint uses port 443, select an unused HTTPS port with `tailscale serve --https=9443 8787`, for example, and include the same port in `MUSE_HUB_TAILSCALE_HOST`. Keep Serve running while testing. Press `Ctrl+C` in the Serve window to stop it.

## Tests

Run this command from the repository root:

```powershell
go test ./...
```
