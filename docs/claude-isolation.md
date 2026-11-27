# Set up an isolated Claude worker

English | [日本語](claude-isolation.ja.md)

Claude Code runs in an isolated Docker container. Sign in with your Claude account. Docker stores that login in a dedicated volume.

## Boundary

- The CLI runs as a non-root user with a read-only root filesystem. Only its dedicated home volume and temporary directory are writable. The container has no host mounts, Docker socket, Hub configuration, or inherited API keys.
- The one-shot prompt disables built-in tools, custom MCP servers, cloud connectors, hooks, skills, and session persistence. The login volume persists Claude account authentication.
- The CLI joins an internal Docker network. A separate proxy allows HTTPS tunnels to the named Anthropic hosts on port 443. It has no published port. The proxy checks the requested CONNECT destination. It does not inspect encrypted HTTP paths.

## Build, verify, and sign in

Use Docker Desktop with Linux containers and Docker Compose. Open PowerShell 7 in the repository root.

```powershell
$claudeCompose = @('-f', 'deploy/claude/compose.yaml')
$env:MUSE_HUB_CLAUDE_AUTH_VOLUME = 'muse-hub-claude-auth'
docker volume create $env:MUSE_HUB_CLAUDE_AUTH_VOLUME
docker compose @claudeCompose build
docker compose @claudeCompose up -d proxy
docker compose @claudeCompose run --rm -T --entrypoint node claude /opt/verify.mjs
```

The check tests the restrictions on the filesystem and inherited credentials, the absence of a default route, denial of an unlisted host and port, and valid TLS connections to two allowed hosts. It does not make model requests.

Sign in inside the container and follow the CLI prompts in your browser. The project uses its own Docker volume for this login.

```powershell
docker compose @claudeCompose run --rm claude auth login --claudeai
```

Run a synthetic prompt. The expected answer is `5`.

```powershell
$claudeSettings = '{"disableAllHooks":true,"disableClaudeAiConnectors":true}'
docker compose @claudeCompose run --rm -T claude --print 'What is 2 + 3? Reply with only the digit.' `
  --tools '' --strict-mcp-config --setting-sources '' --disable-slash-commands `
  --settings $claudeSettings --no-session-persistence --max-turns 1
```

## Connect the worker to the Hub

Keep the Compose proxy running. Follow the [delegation guide](jobs.md#delegate-to-claude) to use Claude for asynchronous jobs.

### Optional synchronous check with `ask`

Keep the Compose proxy running. In the PowerShell session that starts the Hub, set the token as described in the [README](../README.md), then set the name of the login volume and start the server.

```powershell
$env:MUSE_HUB_CLAUDE_ASK_VOLUME = $env:MUSE_HUB_CLAUDE_AUTH_VOLUME
Remove-Item Env:MUSE_HUB_CLAUDE_AUTH_VOLUME
go run ./cmd/muse-hub
```

The Hub checks the container boundary and the Claude login before listening. If a check fails, run the isolation check again and confirm that the `proxy` service is running. Leave the volume setting unset for the default tools.

To test with Muse, follow the [Tailscale Serve setup](usage.md#tailscale-serve) and call `ask` with these arguments:

```json
{"agent":"claude","task":"2 + 3","context":"","data_category":["code"]}
```

The expected result is `{"answer":"5"}`. The `ask` tool accepts only this fixed question.

To shut down the Claude worker, stop the Hub and run `docker compose @claudeCompose down`. To delete the saved login, remove the configured volume after the containers stop.
