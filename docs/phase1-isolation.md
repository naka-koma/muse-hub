# Phase 1 isolation check

English | [日本語](phase1-isolation.ja.md)

Phase 1 checks whether Claude Code can answer a synthetic question from an isolated Docker container. It uses the operator's own Claude sign-in in a dedicated Docker volume. You can first check the container on its own, then enable `ask` in the Hub.

## Boundary

- The CLI runs as a non-root user with a read-only root filesystem. Only its dedicated home volume and temporary directory are writable. The container has no host mounts, Docker socket, Hub configuration, or inherited API keys.
- The one-shot prompt disables built-in tools, custom MCP servers, cloud connectors, hooks, skills, and session persistence. The login volume persists Claude account authentication.
- The CLI joins an internal Docker network. A separate proxy allows HTTPS tunnels to the named Anthropic hosts on port 443. It has no published port. The proxy checks the requested CONNECT destination. It does not inspect encrypted HTTP paths.

## Run the check

Use Docker Desktop with Linux containers and Docker Compose. Open PowerShell 7 in the repository root.

```powershell
$phase1 = @('-p', 'muse-phase1-check', '-f', 'experiments/phase1/compose.yaml')
docker volume create muse-phase1-claude-auth
docker compose @phase1 build
docker compose @phase1 run --rm -T --entrypoint node claude /opt/verify.mjs
```

The check tests the restrictions on the filesystem and inherited credentials, the absence of a default route, denial of an unlisted host and port, and valid TLS connections to two allowed hosts. It does not make model requests.

Sign in inside the container and follow the CLI prompts in your browser. The project uses its own Docker volume for this login.

```powershell
docker compose @phase1 run --rm claude auth login --claudeai
```

Run a synthetic prompt. The expected answer is `5`.

```powershell
$phase1Settings = '{"disableAllHooks":true,"disableClaudeAiConnectors":true}'
docker compose @phase1 run --rm -T claude --print 'What is 2 + 3? Reply with only the digit.' `
  --tools '' --strict-mcp-config --setting-sources '' --disable-slash-commands `
  --settings $phase1Settings --no-session-persistence --max-turns 1
```

## Enable `ask` in the Hub

Keep the Compose proxy running. In the PowerShell session that starts the Hub, set the token as described in the [README](../README.md), then set the name of the login volume and start the server.

```powershell
$env:MUSE_HUB_PHASE1_AUTH_VOLUME = 'muse-phase1-claude-auth'
go run ./cmd/muse-hub
```

The Hub checks the container boundary and the Claude login before listening. If a check fails, run the isolation check again and confirm that the `proxy` service is running. Leave the volume setting unset for the default tools.

To test with Muse, follow the [Tailscale Serve setup](usage.md#tailscale-serve) and call `ask` with these arguments:

```json
{"agent":"claude","task":"2 + 3","context":"","data_category":["code"]}
```

The expected result is `{"answer":"5"}`. Phase 1 accepts only this synthetic request. The Hub sends a fixed prompt to Claude.

When finished, stop the Hub and run `docker compose @phase1 down`. To remove the saved sign-in as well, run `docker volume rm muse-phase1-claude-auth` after the containers stop.
