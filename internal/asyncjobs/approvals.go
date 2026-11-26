package asyncjobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/nakakoma/muse-hub/internal/policy"
)

const ProposalLifetime = 24 * time.Hour

var (
	ErrPolicyNotManaged = errors.New("managed_policy_not_initialized")
	ErrProposalNotFound = errors.New("proposal_not_found")
	ErrProposalClosed   = errors.New("proposal_not_pending")
	ErrProposalExpired  = errors.New("proposal_expired")
	ErrProposalDigest   = errors.New("proposal_revision_mismatch")
	ErrProposalStale    = errors.New("proposal_base_changed")
	ErrProposalNoChange = errors.New("proposal_no_change")
	ErrProposalCapacity = errors.New("proposal_capacity_reached")
)

type PolicyProposal struct {
	ID           string        `json:"proposal_id"`
	BaseRevision string        `json:"base_revision"`
	Revision     string        `json:"revision"`
	Candidate    policy.Config `json:"candidate"`
	Reason       string        `json:"reason"`
	Status       string        `json:"status"`
	CreatedAt    time.Time     `json:"created_at"`
	ExpiresAt    time.Time     `json:"expires_at"`
	DecidedAt    *time.Time    `json:"decided_at,omitempty"`
	base         int64
}

type PolicyWeek struct {
	Since     time.Time        `json:"since"`
	Until     time.Time        `json:"until"`
	Revision  string           `json:"active_revision"`
	Proposed  int64            `json:"proposed"`
	Decisions map[string]int64 `json:"proposal_decisions"`
	Audit     map[string]int64 `json:"policy_decisions"`
}

const proposalColumns = "id,base_generation,base_revision,revision,config,reason,status,created_at,expires_at,decided_at"

func (m *Manager) HasManagedPolicy(ctx context.Context) (bool, error) {
	_, err := m.ManagedPolicy(ctx)
	if errors.Is(err, ErrPolicyNotManaged) {
		return false, nil
	}
	return err == nil, err
}

func (m *Manager) ManagedPolicy(ctx context.Context) (*policy.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.fault {
		return nil, ErrUnavailable
	}
	current, _, err := m.store.activePolicy(ctx)
	return current, err
}

func (m *Manager) InitializePolicy(ctx context.Context, seed *policy.Policy) (*policy.Policy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.fault || seed == nil {
		return nil, ErrUnavailable
	}
	if current, generation, err := m.store.activePolicy(ctx); !errors.Is(err, ErrPolicyNotManaged) {
		if err != nil {
			return nil, err
		}
		return m.applyPendingTightening(ctx, current, generation)
	}
	data, err := encodePolicy(seed)
	if err != nil {
		return nil, err
	}
	if _, err := m.store.conn.ExecContext(ctx, `INSERT INTO managed_policy(id,generation,revision,config) VALUES (1,1,?,?)`,
		seed.Revision(), data); err != nil {
		return nil, ErrUnavailable
	}
	return seed, nil
}

// Only tightenings are considered here, before any worker or HTTP listener starts.
func (m *Manager) applyPendingTightening(ctx context.Context, current *policy.Policy, generation int64) (*policy.Policy, error) {
	now := m.now().UTC().Truncate(time.Millisecond)
	if len(m.active) != 0 || m.store.expireProposals(ctx, now) != nil {
		return nil, ErrUnavailable
	}
	rows, err := m.store.conn.QueryContext(ctx, `SELECT `+proposalColumns+` FROM policy_proposals
		WHERE status='pending' AND base_generation=? AND base_revision=? ORDER BY created_at,id`, generation, current.Revision())
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		candidate, err := policy.FromConfig(item.Candidate)
		if err != nil {
			return nil, ErrUnavailable
		}
		if !candidate.NoBroaderThan(current) {
			continue
		}
		if err := rows.Close(); err != nil {
			return nil, ErrUnavailable
		}
		if _, err := m.setProposalStatus(ctx, item, "auto_applied", now); err != nil {
			return nil, err
		}
		return candidate, nil
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return current, nil
}

func (s *store) activePolicy(ctx context.Context) (*policy.Policy, int64, error) {
	var generation int64
	var revision string
	var data []byte
	err := s.conn.QueryRowContext(ctx, `SELECT generation,revision,config FROM managed_policy WHERE id=1`).Scan(&generation, &revision, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrPolicyNotManaged
	}
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	p, err := decodePolicy(data, revision)
	return p, generation, err
}

func (m *Manager) ProposePolicy(ctx context.Context, base, reason string, candidate *policy.Policy) (PolicyProposal, error) {
	switch reason {
	case "reduce_disclosure", "review_access", "maintenance":
	default:
		return PolicyProposal{}, ErrInvalidInput
	}
	data, err := encodePolicy(candidate)
	if err != nil {
		return PolicyProposal{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.fault {
		return PolicyProposal{}, ErrUnavailable
	}
	current, generation, err := m.store.activePolicy(ctx)
	if err != nil {
		return PolicyProposal{}, err
	}
	if current.Revision() != base {
		return PolicyProposal{}, ErrProposalStale
	}
	if candidate.Revision() == base {
		return PolicyProposal{}, ErrProposalNoChange
	}
	now := m.now().UTC().Truncate(time.Millisecond)
	if m.store.expireProposals(ctx, now) != nil {
		return PolicyProposal{}, ErrUnavailable
	}
	var pending int
	if err := m.store.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM policy_proposals WHERE status='pending'`).Scan(&pending); err != nil {
		return PolicyProposal{}, ErrUnavailable
	}
	if pending >= HistoryLimit {
		return PolicyProposal{}, ErrProposalCapacity
	}
	id, err := newID()
	if err != nil {
		return PolicyProposal{}, err
	}
	expires := now.Add(ProposalLifetime)
	if _, err := m.store.conn.ExecContext(ctx, `INSERT INTO policy_proposals
		(id,base_generation,base_revision,revision,config,reason,status,created_at,expires_at)
		VALUES (?,?,?,?,?,?,'pending',?,?)`, id, generation, base, candidate.Revision(), data, reason, now.UnixMilli(), expires.UnixMilli()); err != nil {
		return PolicyProposal{}, ErrUnavailable
	}
	return PolicyProposal{ID: id, BaseRevision: base, Revision: candidate.Revision(), Candidate: candidate.Config(),
		Reason: reason, Status: "pending", CreatedAt: now, ExpiresAt: expires}, nil
}

func (m *Manager) PolicyProposals(ctx context.Context) ([]PolicyProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.fault || m.store.expireProposals(ctx, m.now()) != nil {
		return nil, ErrUnavailable
	}
	if _, _, err := m.store.activePolicy(ctx); err != nil {
		return nil, err
	}
	rows, err := m.store.conn.QueryContext(ctx, `SELECT `+proposalColumns+` FROM policy_proposals
		ORDER BY (status='pending') DESC, created_at DESC, id DESC LIMIT ?`, HistoryLimit)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	items := make([]PolicyProposal, 0)
	for rows.Next() {
		item, err := scanProposal(rows)
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

func (m *Manager) DecidePolicy(ctx context.Context, id, revision string, approve bool) (PolicyProposal, error) {
	if !isHex(id, 32) || !isHex(revision, 64) {
		return PolicyProposal{}, ErrInvalidInput
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.fault || len(m.active) != 0 {
		return PolicyProposal{}, ErrUnavailable
	}
	now := m.now().UTC().Truncate(time.Millisecond)
	if m.store.expireProposals(ctx, now) != nil {
		return PolicyProposal{}, ErrUnavailable
	}
	item, err := scanProposal(m.store.conn.QueryRowContext(ctx, `SELECT `+proposalColumns+` FROM policy_proposals WHERE id=?`, id))
	if err != nil {
		return PolicyProposal{}, err
	}
	if item.Revision != revision {
		return PolicyProposal{}, ErrProposalDigest
	}
	if item.Status == "expired" {
		return PolicyProposal{}, ErrProposalExpired
	}
	if item.Status != "pending" {
		return PolicyProposal{}, ErrProposalClosed
	}
	current, generation, err := m.store.activePolicy(ctx)
	if err != nil {
		return PolicyProposal{}, err
	}
	if item.BaseRevision != current.Revision() || item.base != generation {
		return PolicyProposal{}, ErrProposalStale
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	return m.setProposalStatus(ctx, item, status, now)
}

func (m *Manager) setProposalStatus(ctx context.Context, item PolicyProposal, status string, now time.Time) (PolicyProposal, error) {
	tx, err := m.store.conn.BeginTx(ctx, nil)
	if err != nil {
		return PolicyProposal{}, ErrUnavailable
	}
	defer tx.Rollback()
	item.Status = status
	if status == "approved" || status == "auto_applied" {
		if err := applyProposal(ctx, tx, item, now.UnixMilli()); err != nil {
			return PolicyProposal{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE policy_proposals SET status=?, decided_at=? WHERE id=?`, item.Status, now.UnixMilli(), item.ID); err != nil {
		return PolicyProposal{}, ErrUnavailable
	}
	if err := tx.Commit(); err != nil {
		return PolicyProposal{}, ErrUnavailable
	}
	item.DecidedAt = &now
	return item, nil
}

func applyProposal(ctx context.Context, tx *sql.Tx, item PolicyProposal, now int64) error {
	steps := []struct {
		query string
		args  []any
	}{
		{`UPDATE managed_policy SET generation=generation+1, revision=?, config=(SELECT config FROM policy_proposals WHERE id=?) WHERE id=1`,
			[]any{item.Revision, item.ID}},
		{`UPDATE review_sessions SET revoked=1 WHERE revision<>? AND revoked=0`, []any{item.Revision}},
		{`UPDATE policy_proposals SET status='superseded',decided_at=? WHERE status='pending' AND id<>?`, []any{now, item.ID}},
	}
	for _, step := range steps {
		if _, err := tx.ExecContext(ctx, step.query, step.args...); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}

func (m *Manager) WeeklyPolicy(ctx context.Context) (PolicyWeek, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC().Truncate(time.Millisecond)
	if m.closed || m.fault || m.store.expireProposals(ctx, now) != nil {
		return PolicyWeek{}, ErrUnavailable
	}
	current, _, err := m.store.activePolicy(ctx)
	if err != nil {
		return PolicyWeek{}, err
	}
	summary := PolicyWeek{Since: now.Add(-7 * 24 * time.Hour), Until: now, Revision: current.Revision()}
	since, until := summary.Since.UnixMilli(), now.UnixMilli()
	if err := m.store.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM policy_proposals WHERE created_at BETWEEN ? AND ?`, since, until).Scan(&summary.Proposed); err != nil {
		return PolicyWeek{}, ErrUnavailable
	}
	summary.Decisions, err = m.store.groupCounts(ctx, `SELECT status,COUNT(*) FROM policy_proposals WHERE decided_at BETWEEN ? AND ? GROUP BY status`, since, until)
	if err != nil {
		return PolicyWeek{}, err
	}
	summary.Audit, err = m.store.groupCounts(ctx, `SELECT outcome,COUNT(*) FROM policy_audit WHERE timestamp BETWEEN ? AND ? GROUP BY outcome`, since, until)
	if err != nil {
		return PolicyWeek{}, err
	}
	return summary, nil
}

func (s *store) groupCounts(ctx context.Context, query string, since, until int64) (map[string]int64, error) {
	rows, err := s.conn.QueryContext(ctx, query, since, until)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	counts := make(map[string]int64)
	for rows.Next() {
		var code string
		var count int64
		if err := rows.Scan(&code, &count); err != nil {
			return nil, ErrUnavailable
		}
		counts[code] = count
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return counts, nil
}

func (s *store) expireProposals(ctx context.Context, now time.Time) error {
	_, err := s.conn.ExecContext(ctx, `UPDATE policy_proposals SET status='expired',decided_at=expires_at
		WHERE status='pending' AND expires_at<=?`, now.UnixMilli())
	return err
}

func scanProposal(row scanner) (PolicyProposal, error) {
	var item PolicyProposal
	var data []byte
	var created, expires int64
	var decided sql.NullInt64
	err := row.Scan(&item.ID, &item.base, &item.BaseRevision, &item.Revision, &data, &item.Reason, &item.Status, &created, &expires, &decided)
	if errors.Is(err, sql.ErrNoRows) {
		return PolicyProposal{}, ErrProposalNotFound
	}
	if err != nil {
		return PolicyProposal{}, ErrUnavailable
	}
	p, err := decodePolicy(data, item.Revision)
	if err != nil {
		return PolicyProposal{}, err
	}
	item.Candidate = p.Config()
	item.CreatedAt, item.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
	if decided.Valid {
		value := time.UnixMilli(decided.Int64).UTC()
		item.DecidedAt = &value
	}
	return item, nil
}

func encodePolicy(p *policy.Policy) ([]byte, error) {
	if p == nil {
		return nil, policy.ErrInvalid
	}
	data, err := json.Marshal(p.Config())
	if err != nil || len(data) > 64*1024 {
		return nil, policy.ErrInvalid
	}
	return data, nil
}

func decodePolicy(data []byte, revision string) (*policy.Policy, error) {
	var config policy.Config
	if len(data) > 64*1024 || json.Unmarshal(data, &config) != nil {
		return nil, ErrUnavailable
	}
	p, err := policy.FromConfig(config)
	if err != nil || p.Revision() != revision {
		return nil, ErrUnavailable
	}
	return p, nil
}
