package dummyworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nakakoma/muse-hub/internal/claude"
	"golang.org/x/sys/windows"
)

const claudeParentArg = "--muse-hub-claude-parent"

// NewNativeClaude validates the executable and dedicated login without starting Docker.
func NewNativeClaude(ctx context.Context, binary, profile string) (func(context.Context, string, string) (string, error), error) {
	if !filepath.IsAbs(binary) || !strings.EqualFold(filepath.Ext(binary), ".exe") {
		return nil, errors.New("MUSE_HUB_CLAUDE_EXE must be the absolute path to a native Windows executable")
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("Claude executable is unavailable")
	}
	info, err := os.Stat(profile)
	if !filepath.IsAbs(profile) || err != nil || !info.IsDir() {
		return nil, errors.New("MUSE_HUB_CLAUDE_CONFIG_DIR must be an existing absolute directory for the Hub login")
	}
	if home, err := os.UserHomeDir(); err == nil {
		defaultProfile := filepath.Join(home, ".claude")
		defaultInfo, statErr := os.Stat(defaultProfile)
		if strings.EqualFold(filepath.Clean(profile), defaultProfile) || (statErr == nil && os.SameFile(info, defaultInfo)) {
			return nil, errors.New("use a separate Claude configuration directory for Muse Hub")
		}
	}
	authStatus := exec.CommandContext(ctx, binary, "auth", "status", "--json")
	authStatus.Dir = profile
	authStatus.Env = claudeEnvironment(profile, profile)
	authStatus.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	authStatus.WaitDelay = time.Second
	data, err := authStatus.Output()
	var status struct {
		LoggedIn bool `json:"loggedIn"`
	}
	if err != nil || json.Unmarshal(data, &status) != nil || !status.LoggedIn {
		return nil, errors.New("sign in to Claude using the dedicated configuration directory")
	}
	busy := make(chan struct{}, 1)
	return func(ctx context.Context, task, reference string) (string, error) {
		select {
		case busy <- struct{}{}:
			defer func() { <-busy }()
		default:
			return "", errors.New("a Claude job is already running")
		}
		prompt, probe := claude.RequestPrompt(task, reference)
		data, err := runProfileWorker(ctx, claudeParentArg, binary, profile, prompt)
		if err != nil {
			return "", err
		}
		return claude.ParseAnswer(data, probe)
	}, nil
}

func runClaudeParent(cancelArg, binary, dir, profile, inputArg string) int {
	input := workerInput(inputArg)
	if input == nil {
		return 1
	}
	defer input.Close()
	cmd := exec.Command(binary, "--print", "--restricted", "--safe-mode", "--tools", "",
		"--disallowedTools", "*", "--strict-mcp-config", "--disable-slash-commands",
		"--settings", `{"disableAllHooks":true,"disableClaudeAiConnectors":true}`,
		"--no-session-persistence", "--max-turns", "1", "--output-format", "json")
	cmd.Dir = dir
	cmd.Env = claudeEnvironment(profile, dir)
	cmd.Stdin = input
	var output boundedClaudeOutput
	cmd.Stdout = &output
	cmd.WaitDelay = time.Second
	return runParent(cancelArg, cmd, func(err error) int {
		if err != nil || os.WriteFile(filepath.Join(dir, workerAnswer), output.Bytes(), 0600) != nil {
			return childFailed
		}
		return childSucceeded
	})
}

func claudeEnvironment(profile, temp string) []string {
	return []string{"SystemRoot=" + os.Getenv("SystemRoot"), "CLAUDE_CONFIG_DIR=" + profile, "TEMP=" + temp, "TMP=" + temp,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "ENABLE_CLAUDEAI_MCP_SERVERS=false"}
}

type boundedClaudeOutput struct{ bytes.Buffer }

func (b *boundedClaudeOutput) Write(data []byte) (int, error) {
	if len(data) > maxWorkerOutput-b.Len() {
		return 0, errors.New("Claude output limit exceeded")
	}
	return b.Buffer.Write(data)
}
