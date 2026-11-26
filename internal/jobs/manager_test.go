package jobs

import (
	"errors"
	"testing"
	"time"
)

func TestStartValidatesDelay(t *testing.T) {
	m := NewManager()
	for _, delay := range []time.Duration{0, 61 * time.Second, -time.Second} {
		if _, err := m.Start(delay); err == nil {
			t.Fatalf("Start(%s) unexpectedly succeeded", delay)
		}
	}
}

func TestStartAndGet(t *testing.T) {
	m := NewManager()
	id, err := m.Start(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Fatalf("job id length=%d, want 32 hex chars", len(id))
	}
	snapshot, err := m.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != Queued && snapshot.Status != Running {
		t.Fatalf("initial status=%q, want queued or running", snapshot.Status)
	}
}

func TestGetUnknownJob(t *testing.T) {
	_, err := NewManager().Get("missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error=%v, want ErrNotFound", err)
	}
}
