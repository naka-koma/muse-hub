package asyncjobs

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReviewResumeRequiresCurrentUnrevokedSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := openTestManager(t, path, time.Second)
	rev := strings.Repeat("a", 64)
	input := ReviewSubmission{RequestID: "review-1", Revision: rev, Rounds: 1,
		Run: func(context.Context, ReviewSession) (string, error) { return "summary", nil }}
	first, err := m.SubmitReview(t.Context(), input)
	if err != nil || first.SessionID == "" || first.JobID == "" {
		t.Fatalf("submit: %v %v", first, err)
	}
	if got := awaitStatus(t, m, first.JobID, Succeeded); got.ReviewSession != first.SessionID {
		t.Fatal("job lost its session binding")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = openTestManager(t, path, time.Second)
	replayed, err := m.SubmitReview(t.Context(), input)
	if err != nil || replayed != first {
		t.Fatalf("restart did not retain dedupe: %v %v", replayed, err)
	}
	input.RequestID, input.SessionID = "review-2", first.SessionID
	if _, err := m.store.conn.ExecContext(t.Context(), `UPDATE review_sessions SET used_grant=1 WHERE id=?`, first.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitReview(t.Context(), input); !errors.Is(err, ErrSessionBlocked) {
		t.Fatalf("temporary-grant session was resumed: %v", err)
	}
	if _, err := m.store.conn.ExecContext(t.Context(), `UPDATE review_sessions SET used_grant=0 WHERE id=?`, first.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := m.InvalidateReviewSessions(t.Context(), strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := m.InvalidateReviewSessions(t.Context(), rev); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SubmitReview(t.Context(), input); !errors.Is(err, ErrSessionBlocked) {
		t.Fatalf("restoring an old policy revived a sealed session: %v", err)
	}
	list, err := m.ListReviewSessions(t.Context(), rev)
	if err != nil || len(list) != 1 || list[0].Resumable || list[0].CompletedRounds != 1 {
		t.Fatalf("bad session metadata: %v %v", list, err)
	}
}
