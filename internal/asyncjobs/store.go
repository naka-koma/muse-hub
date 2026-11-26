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
	result TEXT
);
CREATE INDEX IF NOT EXISTS delegate_jobs_history ON delegate_jobs(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS delegate_jobs_retention ON delegate_jobs(finished_at);`

const snapshotColumns = "id, status, created_at, started_at, finished_at, result"

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
	for _, sql := range []string{"PRAGMA locking_mode=EXCLUSIVE", "PRAGMA synchronous=FULL", schema} {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
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
	var result sql.NullString
	err := row.Scan(&job.JobID, &job.Status, &created, &started, &finished, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	job.CreatedAt = time.UnixMilli(created).UTC()
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
