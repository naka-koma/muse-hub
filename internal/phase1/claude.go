package phase1

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"

	recipe "github.com/nakakoma/muse-hub/experiments/phase1"
	"github.com/nakakoma/muse-hub/internal/tools"
)

const (
	projectName = "muse-phase1-check"
	probeTask   = "2 + 3"
	probePrompt = "What is 2 + 3? Reply with only the digit."
)

var volumeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{1,127}$`)

func ValidVolumeName(name string) bool { return volumeNamePattern.MatchString(name) }

type Claude struct {
	docker string
	volume string
	busy   chan struct{}
}

// NewClaude checks the prepared isolation environment before the Hub listens.
func NewClaude(ctx context.Context, volume string) (*Claude, error) {
	if !ValidVolumeName(volume) {
		return nil, errors.New("invalid Phase 1 Docker volume name")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return nil, errors.New("Phase 1 requires Docker with Compose")
	}
	c := &Claude{docker: docker, volume: volume, busy: make(chan struct{}, 1)}
	data, err := output(exec.CommandContext(ctx, docker, "volume", "inspect", "--format", "{{json .}}", volume))
	var info struct {
		Name    string
		Driver  string
		Options map[string]string
	}
	if err != nil || json.Unmarshal(data, &info) != nil || info.Name != volume || info.Driver != "local" || len(info.Options) != 0 {
		return nil, errors.New("Phase 1 requires a dedicated Docker-managed login volume")
	}
	if _, err := output(exec.CommandContext(ctx, docker, "image", "inspect", "--format", "{{.Id}}", projectName+"-claude")); err != nil {
		return nil, errors.New("Phase 1 requires the prepared Claude image")
	}
	if _, err := c.run(ctx, "node", "/opt/verify.mjs"); err != nil {
		return nil, errors.New("Phase 1 isolation check failed; verify the Compose proxy and networks")
	}
	data, err = c.run(ctx, "claude", "auth", "status", "--json")
	var login struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if err != nil || json.Unmarshal(data, &login) != nil || !login.LoggedIn || login.AuthMethod != "claude.ai" {
		return nil, errors.New("Phase 1 requires sign-in inside the isolated Claude container")
	}
	return c, nil
}

func (c *Claude) Ask(ctx context.Context, input tools.AskInput) (string, error) {
	if !acceptsProbe(input) {
		return "", errors.New("only the Phase 1 synthetic probe is supported")
	}
	select {
	case c.busy <- struct{}{}:
		defer func() { <-c.busy }()
	default:
		return "", errors.New("Phase 1 probe already running")
	}
	data, err := c.run(ctx, "timeout", "--signal=KILL", "30s", "claude", "--print", probePrompt,
		"--tools", "", "--strict-mcp-config", "--setting-sources", "", "--disable-slash-commands",
		"--settings", `{"disableAllHooks":true,"disableClaudeAiConnectors":true}`,
		"--no-session-persistence", "--max-turns", "1", "--output-format", "json")
	if err != nil {
		return "", errors.New("isolated Claude invocation failed")
	}
	return probeAnswer(data)
}

func acceptsProbe(input tools.AskInput) bool {
	return input.Agent == "claude" && strings.TrimSpace(input.Task) == probeTask && input.Context == "" &&
		len(input.DataCategory) == 1 && input.DataCategory[0] == "code"
}

func probeAnswer(data []byte) (string, error) {
	var result struct {
		IsError bool   `json:"is_error"`
		Answer  string `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.IsError || strings.TrimSpace(result.Answer) != "5" {
		return "", errors.New("unexpected Claude probe response")
	}
	return strings.TrimSpace(result.Answer), nil
}

func (c *Claude) run(ctx context.Context, entrypoint string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	name := "muse-phase1-ask-" + hex.EncodeToString(random[:])
	command := append([]string{"compose", "-p", projectName, "-f", "-", "run", "--rm", "-T", "--interactive=false",
		"--no-deps", "--pull", "never", "--name", name, "--entrypoint", entrypoint, "claude"}, args...)
	cmd := exec.CommandContext(ctx, c.docker, command...)
	cmd.Env = append(cmd.Environ(), "COMPOSE_DISABLE_ENV_FILE=1", "MUSE_HUB_PHASE1_AUTH_VOLUME="+c.volume)
	cmd.Stdin = strings.NewReader(recipe.Compose)
	data, err := output(cmd)
	if err != nil {
		// Cancelling the Docker client does not stop the container it started.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, c.docker, "rm", "-f", name).Run()
	}
	return data, err
}

type limitedOutput struct{ buffer bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > 64*1024-b.buffer.Len() {
		return 0, errors.New("command output limit exceeded")
	}
	return b.buffer.Write(p)
}

func output(cmd *exec.Cmd) ([]byte, error) {
	var out limitedOutput
	cmd.Stdout = &out
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.buffer.Bytes(), nil
}
