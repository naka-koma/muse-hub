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
| `MUSE_HUB_JOBS_DB` | Unset | Local SQLite path for [delegation and job results](jobs.md). |
| `MUSE_HUB_DUMMY_SUBPROCESS` | Unset | Set to `1` on Windows to run the same dummy job in a process tree. Requires the job database. |
| `MUSE_HUB_CODEX_EXE` | Unset | Absolute path to the native Windows Codex executable for the [Codex probe](jobs.md#check-codex-delegation-on-windows). |
| `MUSE_HUB_CODEX_HOME` | Unset | Absolute path to the dedicated Codex login directory. Requires the executable and the job database. |
| `MUSE_HUB_CODEX_TEXT` | Unset | Set to `1` to [enable text requests and context](jobs.md#send-text-and-context-to-codex) when no disclosure policy is loaded. |
| `MUSE_HUB_CLAUDE_AUTH_VOLUME` | Unset | Dedicated Docker login volume for [asynchronous Claude jobs](jobs.md#delegate-to-claude). Requires the job database. |
| `MUSE_HUB_CLAUDE_TEXT` | Unset | Set to `1` to accept text requests and context for Claude when no disclosure policy is loaded. |
| `MUSE_HUB_CLAUDE_ASK_VOLUME` | Unset | Dedicated Docker login volume for the optional [synchronous `ask` check](claude-isolation.md#optional-synchronous-check-with-ask). Requires the job database setting to be unset. |
| `MUSE_HUB_POLICY_FILE` | Unset | Local YAML path for the [disclosure policy and audit](policy.md). Requires the job database. |
| `MUSE_HUB_POLICY_APPROVALS` | Unset | Set to `1` to enable [policy proposals and local approval](policy-approval.md). Requires the policy path and job database. |

This server alone cannot determine which authentication format Muse's custom connector supports. If `Authorization: Bearer` does not work, confirm the format Muse supports and adjust the header settings accordingly.

To start without building an executable first, set the token and run `go run ./cmd/muse-hub` from the repository root. If startup fails, check the token length, the configured port, and whether another process is listening on that port.

### Upgrade an existing configuration

Use the new names in the Hub launch environment. Keep the existing SQLite path and Docker login volume. The old names still work as aliases. Startup fails if the two names for the same setting contain different values.

| Previous name | Current name |
| --- | --- |
| `MUSE_HUB_PHASE1_AUTH_VOLUME` | `MUSE_HUB_CLAUDE_ASK_VOLUME` |
| `MUSE_HUB_PHASE2_DB` | `MUSE_HUB_JOBS_DB` |
| `MUSE_HUB_PHASE2_SUBPROCESS` | `MUSE_HUB_DUMMY_SUBPROCESS` |
| `MUSE_HUB_PHASE3_POLICY` | `MUSE_HUB_POLICY_FILE` |
| `MUSE_HUB_PHASE5_APPROVALS` | `MUSE_HUB_POLICY_APPROVALS` |

For Compose setup, use `MUSE_HUB_CLAUDE_AUTH_VOLUME` for the existing login volume, including when you plan to run the synchronous check. See [the Claude setup guide](claude-isolation.md).

## Security and data handling

- The server listens only on `127.0.0.1`. Use Tailscale Serve for access within the tailnet.
- Logs include the tool name, allow or deny outcome, job ID, and duration. They exclude tokens, headers, and `echo` input.
- The default tools work with synthetic inputs and run inside the server. The optional workers have their own setup and input limits.

## Default tools

| Tool | Input | Successful response |
| --- | --- | --- |
| `ping` | None | `status: "ok"` and the current UTC time in ISO 8601 format |
| `echo` | `text`, up to 1,000 characters | The same `text`. Longer input is rejected. |
| `start_dummy_job` | `delay_seconds`, an integer from 1 to 60 | An unpredictable 128-bit `job_id` |
| `get_result` | `job_id` | `queued`, `running`, or `succeeded`; the fixed result and start/completion times are included only when complete |

An unknown ID returns `job_not_found` without a stack trace. Job state and results stay in memory and are lost when the process restarts.
To enable persistent jobs and send requests to Claude or Codex, see [delegation and job results](jobs.md).
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
