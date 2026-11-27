package dummyworker

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	codexParentArg = "--muse-hub-codex-parent"
	codexPrompt    = "What is 2 + 3? Reply with only the digit."
	codexAnswer    = "answer.txt"
)

// NewCodex returns a worker for the fixed arithmetic probe on this PC.
func NewCodex(binary, codexHome string) (func(context.Context) (string, error), error) {
	if !filepath.IsAbs(binary) || !strings.EqualFold(filepath.Ext(binary), ".exe") {
		return nil, errors.New("MUSE_HUB_CODEX_EXE must name an existing native Codex executable by absolute path")
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("Codex executable is unavailable")
	}
	if !filepath.IsAbs(codexHome) {
		return nil, errors.New("MUSE_HUB_CODEX_HOME must be an absolute directory for the dedicated Codex login")
	}
	if info, err := os.Stat(codexHome); err != nil || !info.IsDir() {
		return nil, errors.New("dedicated Codex login directory is unavailable")
	}
	if userHome, err := os.UserHomeDir(); err == nil && strings.EqualFold(filepath.Clean(codexHome), filepath.Join(userHome, ".codex")) {
		return nil, errors.New("use a dedicated Codex login directory for Muse Hub")
	}
	// Codex reads global instructions even when the project instruction limit is zero.
	for _, name := range []string{"AGENTS.md", "AGENTS.override.md"} {
		if _, err := os.Lstat(filepath.Join(codexHome, name)); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("the dedicated Codex login directory must not contain global instructions")
		}
	}
	return func(ctx context.Context) (string, error) {
		return runCodex(ctx, binary, codexHome)
	}, nil
}

func runCodex(ctx context.Context, binary, codexHome string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", errors.New("could not locate the Codex worker")
	}
	dir, err := os.MkdirTemp("", "muse-hub-codex-")
	if err != nil {
		return "", errors.New("could not create the Codex probe directory")
	}
	defer os.RemoveAll(dir)
	tree, err := startTree(exe, codexParentArg, binary, dir, codexHome)
	if err != nil {
		return "", errors.New("could not start the isolated Codex worker")
	}
	defer tree.close()
	exit, err := tree.wait(ctx)
	if err != nil {
		return "", err
	}
	if exit != childSucceeded {
		return "", errors.New("Codex probe did not complete")
	}
	file, err := os.Open(filepath.Join(dir, codexAnswer))
	if err != nil {
		return "", errors.New("Codex probe did not produce an answer")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65))
	if err != nil || len(data) > 64 || strings.TrimSpace(string(data)) != "5" {
		return "", errors.New("unexpected Codex probe answer")
	}
	return "5", nil
}

func runCodexParent(cancelArg, binary, dir, codexHome string) int {
	cmd := exec.Command(binary, "exec", "--ignore-user-config", "--ignore-rules", "--ephemeral",
		"--skip-git-repo-check", "--sandbox", "read-only",
		"--disable", "shell_tool", "--disable", "hooks", "--disable", "plugins", "--disable", "apps",
		"--disable", "remote_plugin", "--disable", "multi_agent", "--disable", "image_generation",
		"--disable", "browser_use", "--disable", "computer_use",
		"--disable", "skill_search", "--disable", "skill_mcp_dependency_install",
		"-c", `approval_policy="never"`, "-c", `web_search="disabled"`,
		"-c", "project_doc_max_bytes=0", "-c", "skills.include_instructions=false",
		"--output-last-message", filepath.Join(dir, codexAnswer), codexPrompt)
	cmd.Dir = dir
	cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "CODEX_HOME=" + codexHome, "TEMP=" + dir, "TMP=" + dir}
	return runParent(cancelArg, cmd, func(err error) bool { return err == nil })
}
