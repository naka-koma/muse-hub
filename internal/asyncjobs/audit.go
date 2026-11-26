package asyncjobs

import "context"

// PolicyAudit holds metadata derived by the Hub, never raw request fields.
type PolicyAudit struct {
	Event    string
	Revision string
	Category string
	Level    string
	Outcome  string
}

type PolicyCounts struct {
	Decisions       int64 `json:"decisions"`
	UnknownCategory int64 `json:"unknown_category"`
}

func (m *Manager) RecordPolicy(ctx context.Context, audit PolicyAudit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.fault {
		return ErrUnavailable
	}
	if _, err := m.store.conn.ExecContext(ctx, `INSERT INTO policy_audit(timestamp,event,revision,category,level,outcome)
		VALUES (?,?,?,?,?,?)`, m.now().UnixMilli(), audit.Event, audit.Revision, audit.Category, audit.Level, audit.Outcome); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Manager) PolicyCounts(ctx context.Context) (PolicyCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var counts PolicyCounts
	if m.closed || m.fault || m.store.conn.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(CASE WHEN outcome='unknown_category' THEN 1 END)
		FROM policy_audit`).Scan(&counts.Decisions, &counts.UnknownCategory) != nil {
		return PolicyCounts{}, ErrUnavailable
	}
	return counts, nil
}
