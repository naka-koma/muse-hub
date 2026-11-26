# Phase 1 isolation check

English | [日本語](phase1-isolation.ja.md)

This experiment checks whether Claude Code can answer a synthetic question from an isolated Docker container. It uses the operator's own Claude sign-in in a dedicated Docker volume. The Hub and Muse are outside this experiment.

## Boundary

- The CLI runs as a non-root user with a read-only root filesystem. Only its dedicated home volume and temporary directory are writable. The container has no host mounts, Docker socket, Hub configuration, or inherited API keys.
- The one-shot prompt disables built-in tools, custom MCP servers, cloud connectors, hooks, skills, and session persistence. The login volume persists Claude account authentication.
- The CLI joins an internal Docker network. A separate proxy allows HTTPS tunnels to the named Anthropic hosts on port 443. It has no published port. The proxy checks the requested CONNECT destination. It does not inspect encrypted HTTP paths.

## Run the check

Use Docker Desktop with Linux containers and Docker Compose. Open PowerShell 7 in the repository root.

```powershell
$phase1 = @('-p', 'muse-phase1-check', '-f', 'experiments/phase1/compose.yaml')
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

Stop the experiment with `docker compose @phase1 down`. Use `docker compose @phase1 down --volumes` if you also want to delete its saved sign-in.
