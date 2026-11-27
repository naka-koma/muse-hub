package claude

import (
	"context"
	"strings"
	"testing"

	"github.com/nakakoma/muse-hub/internal/tools"
)

func TestProbeRejectsOtherInputsBeforeRunningDocker(t *testing.T) {
	adapter := &Claude{}
	for _, input := range []tools.AskInput{
		{Agent: "codex", Task: "2 + 3", DataCategory: []string{"code"}},
		{Agent: "claude", Task: "anything else", DataCategory: []string{"code"}},
		{Agent: "claude", Task: "2 + 3", Context: "extra text", DataCategory: []string{"code"}},
		{Agent: "claude", Task: "2 + 3", DataCategory: []string{"code", "personal"}},
	} {
		if answer, err := adapter.Ask(context.Background(), input); err == nil || answer != "" {
			t.Fatal("non-fixture input was accepted")
		}
	}
}

func TestProbeAnswerRejectsUnapprovedOutput(t *testing.T) {
	for _, body := range []string{`{"result":"private text"}`, `{"is_error":true,"result":"5"}`, `not JSON`} {
		answer, err := probeAnswer([]byte(body))
		if err == nil || answer != "" || strings.Contains(err.Error(), "private text") {
			t.Fatal("unclean response was not rejected")
		}
	}
	if answer, err := probeAnswer([]byte(`{"is_error":false,"result":"5\n"}`)); answer != "5" || err != nil {
		t.Fatalf("expected verified answer: %v", err)
	}
}

func TestValidVolumeName(t *testing.T) {
	if !ValidVolumeName("muse-hub-claude-auth") {
		t.Fatal("default volume name was rejected")
	}
	for _, name := range []string{"../outside", "name:/path", "--help", "name with space", "${ambiguous}"} {
		if ValidVolumeName(name) {
			t.Fatalf("invalid volume name accepted: %q", name)
		}
	}
}
