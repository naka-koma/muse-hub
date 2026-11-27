package main

import (
	"strings"
	"testing"

	"github.com/nakakoma/muse-hub/internal/dummyworker"
)

func TestLoadConfigRequiresToken(t *testing.T) {
	t.Setenv("MUSE_HUB_TOKEN", "")
	t.Setenv("MUSE_HUB_AUTH_HEADER", "")
	t.Setenv("MUSE_HUB_AUTH_SCHEME", "")
	t.Setenv("MUSE_HUB_PORT", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig() succeeded without a token")
	}
}

func TestListenAddressIsLoopbackOnly(t *testing.T) {
	if got := listenAddress(8787); got != "127.0.0.1:8787" {
		t.Fatalf("listenAddress() = %q, want 127.0.0.1:8787", got)
	}
}

func TestLoadConfigAcceptsCurrentAndLegacySettings(t *testing.T) {
	t.Setenv("MUSE_HUB_TOKEN", strings.Repeat("a", 43))
	t.Setenv("MUSE_HUB_CODEX_EXE", "")
	t.Setenv("MUSE_HUB_CODEX_HOME", "")
	t.Setenv("MUSE_HUB_CODEX_TEXT", "")
	t.Setenv("MUSE_HUB_CLAUDE_AUTH_VOLUME", "")
	t.Setenv("MUSE_HUB_CLAUDE_TEXT", "")
	t.Setenv("MUSE_HUB_CLAUDE_ASK_VOLUME", "")
	t.Setenv("MUSE_HUB_PHASE1_AUTH_VOLUME", "")
	names := [][2]string{
		{"MUSE_HUB_JOBS_DB", "MUSE_HUB_PHASE2_DB"},
		{"MUSE_HUB_DUMMY_SUBPROCESS", "MUSE_HUB_PHASE2_SUBPROCESS"},
		{"MUSE_HUB_POLICY_FILE", "MUSE_HUB_PHASE3_POLICY"},
		{"MUSE_HUB_POLICY_APPROVALS", "MUSE_HUB_PHASE5_APPROVALS"},
	}
	for _, pair := range names {
		t.Setenv(pair[0], "")
		t.Setenv(pair[1], "")
	}
	for index, label := range []string{"current", "legacy"} {
		t.Run(label, func(t *testing.T) {
			t.Setenv(names[0][index], "existing.sqlite")
			if dummyworker.Supported {
				t.Setenv(names[1][index], "1")
			}
			t.Setenv(names[2][index], "policy.yaml")
			t.Setenv(names[3][index], "1")
			cfg, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.jobsDB != "existing.sqlite" || cfg.dummySubprocess != dummyworker.Supported ||
				cfg.policyFile != "policy.yaml" || !cfg.policyApprovals {
				t.Fatal("job and policy settings were not loaded")
			}
		})
	}
	for _, name := range []string{"MUSE_HUB_CLAUDE_ASK_VOLUME", "MUSE_HUB_PHASE1_AUTH_VOLUME"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "claude-login")
			cfg, err := loadConfig()
			if err != nil || cfg.claudeAskVolume != "claude-login" {
				t.Fatalf("ask volume was not loaded: %v", err)
			}
		})
	}
}

func TestJobsDBSettingRejectsConflictingNames(t *testing.T) {
	t.Setenv("MUSE_HUB_JOBS_DB", "existing.sqlite")
	t.Setenv("MUSE_HUB_PHASE2_DB", "existing.sqlite")
	if value, err := jobsDBSetting(); err != nil || value != "existing.sqlite" {
		t.Fatalf("same setting failed: %q, %v", value, err)
	}
	t.Setenv("MUSE_HUB_PHASE2_DB", "other.sqlite")
	if _, err := jobsDBSetting(); err == nil {
		t.Fatal("different databases under current and former names were accepted")
	}
}
