package dummyworker

import (
	"context"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestStopWaitsForParentAndChild(t *testing.T) {
	t.Setenv("MUSE_HUB_TOKEN", "synthetic-must-not-inherit")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := startTree(exe, 0, "-test.run=^TestProcessTreeFixture$", "--", "parent")
	if err != nil {
		t.Fatal(err)
	}
	defer tree.close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		active, err := tree.active()
		if err != nil {
			t.Fatal(err)
		}
		if active == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected two real processes in the job, found %d", active)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tree.stop(); err != nil {
		t.Fatal(err)
	}
	if active, err := tree.active(); err != nil || active != 0 {
		t.Fatalf("job still has active processes: %d, %v", active, err)
	}
	if signaled, err := windows.WaitForSingleObject(tree.process, 0); err != nil || signaled != windows.WAIT_OBJECT_0 {
		t.Fatalf("parent has not exited: %d, %v", signaled, err)
	}
}

func TestProcessTreeFixture(t *testing.T) {
	if len(os.Args) < 4 || os.Args[2] != "--" {
		return
	}
	if os.Getenv("MUSE_HUB_TOKEN") != "" {
		os.Exit(2)
	}
	switch os.Args[3] {
	case "parent":
		if len(os.Args) != 5 {
			os.Exit(1)
		}
		os.Exit(runDummyParent(os.Args[4], "-test.run=^TestProcessTreeFixture$", "--", "child"))
	case "child":
		<-time.After(10 * time.Second)
		os.Exit(5)
	}
}

func TestCancelledContextDoesNotStartFixture(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Run(ctx); err != context.Canceled {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestCodexRateLimitMessageCanSpanWrites(t *testing.T) {
	var diagnostic codexDiagnostics
	_, _ = diagnostic.Write([]byte("ERROR: exceeded retry limit, last status: 429"))
	_, _ = diagnostic.Write([]byte(" Too Many Requests\n"))
	if !diagnostic.rateLimited {
		t.Fatal("split Codex rate-limit message was not recognized")
	}
}
