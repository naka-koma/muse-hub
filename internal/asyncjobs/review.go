package asyncjobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

const MaxReviewRounds = 3

var (
	ErrSessionNotFound = errors.New("session_not_found")
	ErrSessionBlocked  = errors.New("session_restart_required")
)

type ReviewSession struct {
	ID              string    `json:"session_id"`
	CompletedRounds int       `json:"completed_rounds"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Resumable       bool      `json:"resumable"`
	ProposerID      string    `json:"-"`
	ReviewerID      string    `json:"-"`
	revision        string
	usedGrant       bool
	revoked         bool
}

type ReviewReceipt struct {
	JobID     string `json:"job_id"`
	SessionID string `json:"session_id"`
}

type ReviewSubmission struct {
	RequestID string
	SessionID string
	Revision  string
	Rounds    int
	Run       func(context.Context, ReviewSession) (string, error)
}

const reviewSessionColumns = "id, revision, used_grant, revoked, proposer_id, reviewer_id, completed_rounds, created_at, updated_at"

func readReviewSession(row scanner, revision string) (ReviewSession, error) {
	var session ReviewSession
	var created, updated int64
	err := row.Scan(&session.ID, &session.revision, &session.usedGrant, &session.revoked, &session.ProposerID, &session.ReviewerID,
		&session.CompletedRounds, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewSession{}, ErrSessionNotFound
	}
	if err != nil {
		return ReviewSession{}, ErrUnavailable
	}
	session.CreatedAt, session.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	session.Resumable = !session.usedGrant && !session.revoked && revision == session.revision
	return session, nil
}

func (s *store) reviewSession(ctx context.Context, id, revision string) (ReviewSession, error) {
	return readReviewSession(s.conn.QueryRowContext(ctx, `SELECT `+reviewSessionColumns+` FROM review_sessions WHERE id=?`, id), revision)
}

func (m *Manager) SubmitReview(ctx context.Context, input ReviewSubmission) (ReviewReceipt, error) {
	if !idPattern.MatchString(input.RequestID) || !isHex(input.Revision, 64) ||
		(input.SessionID != "" && !isHex(input.SessionID, 32)) ||
		input.Rounds < 1 || input.Rounds > MaxReviewRounds || input.Run == nil {
		return ReviewReceipt{}, ErrInvalidInput
	}
	key := sha256.Sum256([]byte(input.RequestID))
	fingerprint := sha256.Sum256([]byte("review-v1\x00" + input.SessionID + "\x00" + strconv.Itoa(input.Rounds)))
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ready(ctx); err != nil {
		return ReviewReceipt{}, err
	}
	prior, err := m.store.priorJob(ctx, key, fingerprint)
	if err != nil {
		return ReviewReceipt{}, err
	}
	sessionID := input.SessionID
	if prior != "" {
		if err := m.store.conn.QueryRowContext(ctx, `SELECT session_id FROM review_jobs WHERE job_id=?`, prior).Scan(&sessionID); err != nil {
			return ReviewReceipt{}, ErrUnavailable
		}
	}
	var session ReviewSession
	if sessionID != "" {
		session, err = m.store.reviewSession(ctx, sessionID, input.Revision)
		if err != nil {
			return ReviewReceipt{}, err
		}
		if !session.Resumable {
			return ReviewReceipt{}, ErrSessionBlocked
		}
	}
	if prior != "" {
		return ReviewReceipt{JobID: prior, SessionID: session.ID}, nil
	}
	if err := m.checkCapacity(session.ID); err != nil {
		return ReviewReceipt{}, err
	}
	id, err := newID()
	if err != nil {
		return ReviewReceipt{}, err
	}
	create := session.ID == ""
	if create {
		for _, target := range []*string{&session.ID, &session.ProposerID, &session.ReviewerID} {
			*target, err = newID()
			if err != nil {
				return ReviewReceipt{}, err
			}
		}
	}
	now := m.now().UnixMilli()
	tx, err := m.store.conn.BeginTx(ctx, nil)
	if err != nil {
		return ReviewReceipt{}, ErrUnavailable
	}
	defer tx.Rollback()
	if create {
		_, err = tx.ExecContext(ctx, `INSERT INTO review_sessions(id,revision,proposer_id,reviewer_id,created_at,updated_at)
			VALUES (?,?,?,?,?,?)`, session.ID, input.Revision, session.ProposerID, session.ReviewerID, now, now)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE review_sessions SET updated_at=? WHERE id=?`, now, session.ID)
	}
	if err != nil {
		return ReviewReceipt{}, ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO delegate_jobs(id,request_key,fingerprint,status,created_at)
		VALUES (?,?,?,'queued',?)`, id, key[:], fingerprint[:], now); err != nil {
		return ReviewReceipt{}, ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO review_jobs(job_id,session_id) VALUES (?,?)`, id, session.ID); err != nil {
		return ReviewReceipt{}, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return ReviewReceipt{}, ErrUnavailable
	}
	m.start(id, session.ID, input.Rounds, func(ctx context.Context) (string, error) { return input.Run(ctx, session) })
	return ReviewReceipt{JobID: id, SessionID: session.ID}, nil
}

func (m *Manager) ListReviewSessions(ctx context.Context, revision string) ([]ReviewSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ready(ctx); err != nil {
		return nil, err
	}
	rows, err := m.store.conn.QueryContext(ctx, `SELECT `+reviewSessionColumns+` FROM review_sessions
		ORDER BY updated_at DESC, id DESC LIMIT ?`, HistoryLimit)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	items := make([]ReviewSession, 0)
	for rows.Next() {
		item, err := readReviewSession(rows, revision)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return items, nil
}

// InvalidateReviewSessions seals continuations after a policy revision change.
// Restoring an old policy must not reopen sessions previously affected by a change.
func (m *Manager) InvalidateReviewSessions(ctx context.Context, revision string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !isHex(revision, 64) || m.ready(ctx) != nil {
		return ErrUnavailable
	}
	if _, err := m.store.conn.ExecContext(ctx, `UPDATE review_sessions SET revoked=1 WHERE revision<>? AND revoked=0`, revision); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *store) priorJob(ctx context.Context, key, fingerprint [32]byte) (string, error) {
	var id string
	var prior []byte
	err := s.conn.QueryRowContext(ctx, `SELECT id,fingerprint FROM delegate_jobs WHERE request_key=?`, key[:]).Scan(&id, &prior)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", ErrUnavailable
	}
	if !bytes.Equal(prior, fingerprint[:]) {
		return "", ErrConflict
	}
	return id, nil
}

func (s *store) finish(ctx context.Context, id string, result any, status Status, job *activeJob, now time.Time) error {
	// Keep free-text output out of the legacy field read by older Hub versions.
	const update = `UPDATE delegate_jobs SET status=?1, finished_at=?2,
		result=CASE WHEN unverified=0 THEN ?3 END,
		codex_text_result=CASE WHEN unverified=1 THEN ?3 END WHERE id=?4`
	if job.reviewRounds == 0 || status != Succeeded {
		_, err := s.conn.ExecContext(ctx, update, status, now.UnixMilli(), result, id)
		return err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, update, status, now.UnixMilli(), result, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE review_sessions SET completed_rounds=completed_rounds+?, updated_at=? WHERE id=?`,
		job.reviewRounds, now.UnixMilli(), job.session); err != nil {
		return err
	}
	return tx.Commit()
}

func isHex(s string, size int) bool {
	if len(s) != size {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
