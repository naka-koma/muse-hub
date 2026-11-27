package asyncjobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTestManager(t *testing.T, path string, timeout time.Duration) *Manager {
	t.Helper()
	m, err := Open(t.Context(), path, timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func submit(t *testing.T, m *Manager, input Submission) string {
	t.Helper()
	id, err := m.Submit(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func awaitStatus(t *testing.T, m *Manager, id string, want Status) Snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, err := m.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == want {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job did not reach %s", want)
	return Snapshot{}
}

func TestDeduplicationSessionAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := openTestManager(t, path, 10*time.Second)
	started, stopping, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	defer release()
	input := Submission{RequestID: "retry-1", Session: "session-1", WorkKey: "fixture", Run: func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		close(stopping)
		<-released
		return "late answer", nil
	}}
	callCtx, disconnect := context.WithCancel(t.Context())
	id, err := m.Submit(callCtx, input)
	disconnect()
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if retryID := submit(t, m, input); retryID != id {
		t.Fatal("same request created a new job")
	}
	conflict := input
	conflict.WorkKey = "another-fixture"
	if _, err := m.Submit(t.Context(), conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("request reuse with different input: %v", err)
	}
	conflict.RequestID = "retry-2"
	if _, err := m.Submit(t.Context(), conflict); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("overlapping session: %v", err)
	}
	if other, err := Open(t.Context(), path, time.Second); !errors.Is(err, ErrUnavailable) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("database had a second owner: %v", err)
	}

	cancelCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	confirmed := make(chan error, 1)
	go func() {
		job, err := m.Cancel(cancelCtx, id)
		if err == nil && (job.Status != Cancelled || job.Result != nil) {
			err = errors.New("cancellation returned an unexpected status or output")
		}
		confirmed <- err
	}()
	<-stopping
	if job, err := m.Get(t.Context(), id); err != nil || job.Status != Running {
		t.Fatalf("cancellation was recorded before the worker stopped: %v, %v", job.Status, err)
	}
	release()
	if err := <-confirmed; err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	newManager := openTestManager(t, path, time.Second)
	if retryID := submit(t, newManager, input); retryID != id {
		t.Fatal("retry after restart created another job")
	}
	job, err := newManager.Get(t.Context(), id)
	if err != nil || job.Status != Cancelled {
		t.Fatalf("cancellation did not persist: %v, %v", job.Status, err)
	}
}

func TestCapacityFailureTimeoutAndShutdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := openTestManager(t, path, time.Second)
	waitForStop := func(ctx context.Context) (string, error) { <-ctx.Done(); return "", ctx.Err() }
	var ids []string
	for n := range MaxConcurrent {
		ids = append(ids, submit(t, m, Submission{RequestID: string(rune('a' + n)), WorkKey: "fixture", Run: waitForStop}))
	}
	if _, err := m.Submit(t.Context(), Submission{RequestID: "extra", WorkKey: "fixture", Run: waitForStop}); !errors.Is(err, ErrAtCapacity) {
		t.Fatalf("capacity check: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = openTestManager(t, path, 40*time.Millisecond)
	for _, id := range ids {
		awaitStatus(t, m, id, Interrupted)
	}
	timeout := submit(t, m, Submission{RequestID: "timeout", WorkKey: "fixture", Run: waitForStop})
	awaitStatus(t, m, timeout, TimedOut)
	failure := submit(t, m, Submission{RequestID: "failure", WorkKey: "fixture", Run: func(context.Context) (string, error) {
		return "private data", errors.New("private error")
	}})
	if job := awaitStatus(t, m, failure, Failed); job.Result != nil {
		t.Fatal("failed job retained output")
	}
}

func TestRecoveryAndRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := openTestManager(t, path, time.Second)
	now := time.Now()
	for _, status := range []Status{Queued, Running, Succeeded} {
		id := string(status) + "-simulated-db-row"
		hash := sha256.Sum256([]byte(id))
		var finish any
		if status == Succeeded {
			finish = now.Add(-Retention - time.Second).UnixMilli()
		}
		if _, err := m.store.conn.ExecContext(t.Context(), `INSERT INTO delegate_jobs
			(id, request_key, fingerprint, status, created_at, finished_at) VALUES (?,?,?,?,?,?)`,
			id, hash[:], hash[:], status, now.Add(-Retention-time.Hour).UnixMilli(), finish); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = openTestManager(t, path, time.Second)
	list, err := m.List(t.Context())
	if err != nil || len(list) != 2 {
		t.Fatalf("expected two retained interrupted rows: %v, %v", list, err)
	}
	for _, job := range list {
		if job.Status != Interrupted || job.FinishedAt == nil || job.Result != nil {
			t.Fatalf("unexpected recovery row: %v", job)
		}
	}
}

func TestStopUnconfirmedDoesNotReportCancelled(t *testing.T) {
	m := openTestManager(t, filepath.Join(t.TempDir(), "jobs.sqlite"), time.Second)
	started := make(chan struct{})
	id := submit(t, m, Submission{RequestID: "stop-unconfirmed", WorkKey: "fixture", Run: func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ErrStopUnconfirmed
	}})
	<-started
	job, err := m.Cancel(t.Context(), id)
	if err != nil || job.Status != Failed || job.Result != nil {
		t.Fatalf("unconfirmed stop was reported as success: %v, %v", job, err)
	}
}

func TestCodexAgentPersistsAlongsideLegacyJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	original := openTestManager(t, path, time.Second)
	run := func(context.Context) (string, error) { return "5", nil }
	legacyInput := Submission{RequestID: "legacy", WorkKey: "synthetic-sum-v1", Run: run}
	legacyID := submit(t, original, legacyInput)
	awaitStatus(t, original, legacyID, Succeeded)
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	// Reproduce a database written before jobs recorded an agent name.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, alterErr := db.ExecContext(t.Context(), "ALTER TABLE delegate_jobs DROP COLUMN agent")
	if err := errors.Join(alterErr, db.Close()); err != nil {
		t.Fatal(err)
	}

	upgraded := openTestManager(t, path, time.Second)
	if got := submit(t, upgraded, legacyInput); got != legacyID {
		t.Fatal("upgrading the database changed the retry identity of the legacy job")
	}
	if job := awaitStatus(t, upgraded, legacyID, Succeeded); job.Agent != "dummy" {
		t.Fatal("legacy job did not retain the dummy agent")
	}
	codexID := submit(t, upgraded, Submission{
		RequestID: "codex", WorkKey: "codex-synthetic-sum-v1", Agent: "codex", Run: run,
	})
	if job := awaitStatus(t, upgraded, codexID, Succeeded); job.Agent != "codex" || job.Result == nil || *job.Result != "5" {
		t.Fatal("Codex result or agent was not stored")
	}
}
