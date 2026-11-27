package asyncjobs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `CREATE TABLE IF NOT EXISTS delegate_jobs (
	id TEXT PRIMARY KEY,
	request_key BLOB NOT NULL UNIQUE,
	fingerprint BLOB NOT NULL,
	status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','cancelled','timed_out','interrupted')),
	created_at INTEGER NOT NULL,
	started_at INTEGER,
	finished_at INTEGER,
	result TEXT,
	agent TEXT NOT NULL DEFAULT 'dummy' CHECK(agent IN ('dummy', 'codex'))
);
CREATE INDEX IF NOT EXISTS delegate_jobs_history ON delegate_jobs(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS delegate_jobs_retention ON delegate_jobs(finished_at);
CREATE TABLE IF NOT EXISTS policy_audit (
	id INTEGER PRIMARY KEY,
	timestamp INTEGER NOT NULL,
	event TEXT NOT NULL,
	revision TEXT NOT NULL,
	category TEXT NOT NULL,
	level TEXT NOT NULL,
	outcome TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS review_sessions (
	id TEXT PRIMARY KEY,
	revision TEXT NOT NULL,
	used_grant INTEGER NOT NULL DEFAULT 0,
	revoked INTEGER NOT NULL DEFAULT 0,
	proposer_id TEXT NOT NULL,
	reviewer_id TEXT NOT NULL,
	completed_rounds INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS review_jobs (
	job_id TEXT PRIMARY KEY REFERENCES delegate_jobs(id) ON DELETE CASCADE,
	session_id TEXT NOT NULL REFERENCES review_sessions(id)
);
CREATE TABLE IF NOT EXISTS managed_policy (
	id INTEGER PRIMARY KEY CHECK(id=1),
	generation INTEGER NOT NULL,
	revision TEXT NOT NULL,
	config BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS policy_proposals (
	id TEXT PRIMARY KEY,
	base_generation INTEGER NOT NULL,
	base_revision TEXT NOT NULL,
	revision TEXT NOT NULL,
	config BLOB NOT NULL,
	reason TEXT NOT NULL,
	status TEXT NOT NULL CHECK(status IN ('pending','approved','auto_applied','rejected','expired','superseded')),
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	decided_at INTEGER
);`

const snapshotColumns = "j.id, j.status, j.created_at, j.started_at, j.finished_at, j.result, r.session_id, j.agent"

type store struct {
	db   *sql.DB
	conn *sql.Conn
}

func openStore(ctx context.Context, path string, now time.Time) (*store, error) {
	if path == "" {
		return nil, ErrUnavailable
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	db, err := sql.Open("sqlite", absolute)
	if err != nil {
		return nil, ErrUnavailable
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, ErrUnavailable
	}
	s := &store{db: db, conn: conn}
	// Keep the exclusive SQLite lock on this connection until shutdown. Another
	// Hub must not mark this process's queued or running jobs as interrupted.
	for _, sql := range []string{"PRAGMA locking_mode=EXCLUSIVE", "PRAGMA synchronous=FULL", "PRAGMA foreign_keys=ON", schema} {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			_ = s.close()
			return nil, ErrUnavailable
		}
	}
	var agentColumn int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('delegate_jobs') WHERE name='agent'`).Scan(&agentColumn); err != nil {
		_ = s.close()
		return nil, ErrUnavailable
	}
	if agentColumn == 0 {
		if _, err := conn.ExecContext(ctx, `ALTER TABLE delegate_jobs ADD COLUMN agent TEXT NOT NULL DEFAULT 'dummy' CHECK(agent IN ('dummy', 'codex'))`); err != nil {
			_ = s.close()
			return nil, ErrUnavailable
		}
	}
	if _, err := conn.ExecContext(ctx, `UPDATE delegate_jobs SET status='interrupted', finished_at=?
		WHERE status IN ('queued','running')`, now.UnixMilli()); err != nil {
		_ = s.close()
		return nil, ErrUnavailable
	}
	if err := s.prune(ctx, now); err != nil {
		_ = s.close()
		return nil, ErrUnavailable
	}
	return s, nil
}

func (s *store) close() error { return errors.Join(s.conn.Close(), s.db.Close()) }

func (s *store) prune(ctx context.Context, now time.Time) error {
	_, err := s.conn.ExecContext(ctx, `DELETE FROM delegate_jobs WHERE finished_at <= ?`, now.Add(-Retention).UnixMilli())
	return err
}

type scanner interface{ Scan(...any) error }

func readSnapshot(row scanner) (Snapshot, error) {
	var job Snapshot
	var created int64
	var started, finished sql.NullInt64
	var result, session sql.NullString
	err := row.Scan(&job.JobID, &job.Status, &created, &started, &finished, &result, &session, &job.Agent)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	job.CreatedAt = time.UnixMilli(created).UTC()
	job.ReviewSession = session.String
	if started.Valid {
		timestamp := time.UnixMilli(started.Int64).UTC()
		job.StartedAt = &timestamp
	}
	if finished.Valid {
		timestamp := time.UnixMilli(finished.Int64).UTC()
		job.FinishedAt = &timestamp
	}
	if result.Valid {
		job.Result = &result.String
	}
	return job, nil
}
