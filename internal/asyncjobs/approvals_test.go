package asyncjobs

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nakakoma/muse-hub/internal/policy"
)

func policyFixture(t *testing.T, lvl policy.Level, consult bool) *policy.Policy {
	t.Helper()
	p, err := policy.FromConfig(policy.Config{Version: 1, Data: map[string]policy.Level{"code": lvl},
		Execution: policy.Execution{Read: true, Consult: consult}, Resources: []string{"arithmetic"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func proposeFixture(t *testing.T, m *Manager, base, candidate *policy.Policy) PolicyProposal {
	t.Helper()
	proposal, err := m.ProposePolicy(t.Context(), base.Revision(), "review_access", candidate)
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}

func TestApprovalBindsCandidateAndSealsReviewSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	m := openTestManager(t, path, time.Second)
	seed := policyFixture(t, policy.Full, true)
	if p, err := m.InitializePolicy(t.Context(), seed); err != nil || p.Revision() != seed.Revision() {
		t.Fatalf("initialize: %v", err)
	}
	job, err := m.SubmitReview(t.Context(), ReviewSubmission{RequestID: "review", Revision: seed.Revision(), Rounds: 1,
		Run: func(context.Context, ReviewSession) (string, error) { return "synthetic review", nil }})
	if err != nil {
		t.Fatal(err)
	}
	awaitStatus(t, m, job.JobID, Succeeded)
	clean := policyFixture(t, policy.Summary, true)
	change := proposeFixture(t, m, seed, clean)
	stale := proposeFixture(t, m, seed, policyFixture(t, policy.None, false))
	if current, err := m.ManagedPolicy(t.Context()); err != nil || current.Revision() != seed.Revision() {
		t.Fatalf("proposal changed the active policy: %v", err)
	}
	if _, err := m.DecidePolicy(t.Context(), change.ID, strings.Repeat("0", 64), true); !errors.Is(err, ErrProposalDigest) {
		t.Fatalf("unexpected revision was accepted: %v", err)
	}
	if _, err := m.store.conn.ExecContext(t.Context(), `UPDATE policy_proposals SET config=? WHERE id=?`,
		`{"version":1,"data":{"code":"full"},"execution":{"consult":false,"read":true},"resources":["arithmetic"]}`, change.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.DecidePolicy(t.Context(), change.ID, change.Revision, true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("modified candidate was approved: %v", err)
	}
	data, err := encodePolicy(clean)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.conn.ExecContext(t.Context(), `UPDATE policy_proposals SET config=? WHERE id=?`, data, change.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := m.DecidePolicy(t.Context(), change.ID, change.Revision, true); err != nil || result.Status != "approved" {
		t.Fatalf("approve: %v %v", result.Status, err)
	}
	if _, err := m.DecidePolicy(t.Context(), stale.ID, stale.Revision, true); !errors.Is(err, ErrProposalClosed) {
		t.Fatalf("proposal based on a superseded policy was approved: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m = openTestManager(t, path, time.Second)
	if current, err := m.InitializePolicy(t.Context(), seed); err != nil || current.Revision() != clean.Revision() {
		t.Fatalf("seed replaced active policy: %v", err)
	}
	if _, err := m.DecidePolicy(t.Context(), change.ID, change.Revision, true); !errors.Is(err, ErrProposalClosed) {
		t.Fatalf("approval could be replayed: %v", err)
	}
	back := proposeFixture(t, m, clean, seed)
	if _, err := m.DecidePolicy(t.Context(), back.ID, back.Revision, true); err != nil {
		t.Fatal(err)
	}
	list, err := m.ListReviewSessions(t.Context(), seed.Revision())
	if err != nil || len(list) != 1 || list[0].Resumable {
		t.Fatalf("restoring an earlier policy revived a session: %v %v", list, err)
	}
}

func TestApprovalExpiryRejectionAndWeeklyCounts(t *testing.T) {
	m := openTestManager(t, filepath.Join(t.TempDir(), "jobs.sqlite"), time.Second)
	now := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	seed := policyFixture(t, policy.Summary, true)
	candidate := policyFixture(t, policy.Full, true)
	if _, err := m.InitializePolicy(t.Context(), seed); err != nil {
		t.Fatal(err)
	}
	now = now.Add(-8 * 24 * time.Hour)
	if err := m.RecordPolicy(t.Context(), PolicyAudit{Outcome: "allowed"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(8 * 24 * time.Hour)
	expiring := proposeFixture(t, m, seed, candidate)
	now = now.Add(ProposalLifetime)
	if _, err := m.DecidePolicy(t.Context(), expiring.ID, expiring.Revision, true); !errors.Is(err, ErrProposalExpired) {
		t.Fatalf("expired proposal accepted: %v", err)
	}
	reject := proposeFixture(t, m, seed, candidate)
	if _, err := m.DecidePolicy(t.Context(), reject.ID, reject.Revision, false); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordPolicy(t.Context(), PolicyAudit{Outcome: "unknown_category"}); err != nil {
		t.Fatal(err)
	}
	summary, err := m.WeeklyPolicy(t.Context())
	if err != nil || summary.Proposed != 2 || summary.Decisions["expired"] != 1 || summary.Decisions["rejected"] != 1 ||
		summary.Audit["unknown_category"] != 1 || summary.Audit["allowed"] != 0 || summary.Revision != seed.Revision() {
		t.Fatalf("unexpected weekly summary: %v %v", summary, err)
	}
}

func TestOnlyTighteningAppliesAtStartup(t *testing.T) {
	m := openTestManager(t, filepath.Join(t.TempDir(), "jobs.sqlite"), time.Second)
	seed := policyFixture(t, policy.Summary, true)
	wider := policyFixture(t, policy.Full, true)
	narrower := policyFixture(t, policy.Summary, false)
	if _, err := m.InitializePolicy(t.Context(), seed); err != nil {
		t.Fatal(err)
	}
	proposeFixture(t, m, seed, wider)
	if current, err := m.InitializePolicy(t.Context(), seed); err != nil || current.Revision() != seed.Revision() {
		t.Fatalf("startup unexpectedly broadened the policy: %v", err)
	}
	proposeFixture(t, m, seed, narrower)
	if current, err := m.InitializePolicy(t.Context(), seed); err != nil || current.Revision() != narrower.Revision() {
		t.Fatalf("startup did not apply a safe tightening: %v", err)
	}
	summary, err := m.WeeklyPolicy(t.Context())
	if err != nil || summary.Decisions["auto_applied"] != 1 || summary.Decisions["superseded"] != 1 {
		t.Fatalf("automatic change was not recorded: %v %v", summary, err)
	}
}
