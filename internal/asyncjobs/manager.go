package asyncjobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"sync"
	"time"
)

const (
	MaxConcurrent = 5
	HistoryLimit  = 100
	Retention     = 7 * 24 * time.Hour
)

var (
	ErrNotFound        = errors.New("job_not_found")
	ErrAtCapacity      = errors.New("job_capacity_reached")
	ErrSessionBusy     = errors.New("session_busy")
	ErrConflict        = errors.New("request_id_conflict")
	ErrInvalidInput    = errors.New("invalid_job_input")
	ErrUnavailable     = errors.New("job_store_unavailable")
	ErrCancelWait      = errors.New("job_stop_not_confirmed")
	ErrStopUnconfirmed = errors.New("worker process stop not confirmed")
	errCanceled        = errors.New("job canceled by request")
	errInterrupted     = errors.New("hub shutting down")
	idPattern          = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

type Status string

const (
	Queued      Status = "queued"
	Running     Status = "running"
	Succeeded   Status = "succeeded"
	Failed      Status = "failed"
	Cancelled   Status = "cancelled"
	TimedOut    Status = "timed_out"
	Interrupted Status = "interrupted"
)

type Snapshot struct {
	JobID         string     `json:"job_id"`
	Status        Status     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Result        *string    `json:"result,omitempty"`
	ReviewSession string     `json:"session_id,omitempty"`
	Agent         string     `json:"-"`
	Unverified    bool       `json:"-"`
}

type Submission struct {
	RequestID  string
	Session    string
	WorkKey    string
	Agent      string
	Unverified bool
	// Run must return only after its work has stopped, or report ErrStopUnconfirmed.
	Run func(context.Context) (string, error)
}

type activeJob struct {
	session      string
	reviewRounds int
	cancel       context.CancelCauseFunc
	done         chan struct{}
}

type Manager struct {
	mu       sync.Mutex
	store    *store
	timeout  time.Duration
	now      func() time.Time
	active   map[string]*activeJob
	workers  sync.WaitGroup
	closed   bool
	fault    bool
	close    sync.Once
	closeErr error
}

func Open(ctx context.Context, path string, timeout time.Duration) (*Manager, error) {
	if timeout <= 0 {
		return nil, ErrInvalidInput
	}
	s, err := openStore(ctx, path, time.Now())
	if err != nil {
		return nil, err
	}
	return &Manager{store: s, timeout: timeout, now: time.Now, active: make(map[string]*activeJob)}, nil
}

func (m *Manager) Submit(ctx context.Context, input Submission) (string, error) {
	if input.Agent == "" {
		input.Agent = "dummy"
	}
	if !idPattern.MatchString(input.RequestID) || (input.Session != "" && !idPattern.MatchString(input.Session)) ||
		input.WorkKey == "" || input.Run == nil || (input.Agent != "dummy" && input.Agent != "codex" && input.Agent != "claude") {
		return "", ErrInvalidInput
	}
	requestKey := sha256.Sum256([]byte(input.RequestID))
	fingerprint := sha256.Sum256([]byte(input.Session + "\x00" + input.WorkKey))
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ready(ctx); err != nil {
		return "", err
	}
	priorID, err := m.store.priorJob(ctx, requestKey, fingerprint)
	if err != nil || priorID != "" {
		return priorID, err
	}
	if err := m.checkCapacity(input.Session); err != nil {
		return "", err
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	legacyAgent := input.Agent
	if input.Agent == "claude" {
		// Existing databases constrain this column to the original two agents.
		legacyAgent = "dummy"
	}
	if _, err := m.store.conn.ExecContext(ctx, `INSERT INTO delegate_jobs(id, request_key, fingerprint, status, created_at, agent, unverified, delegation_agent)
		VALUES (?, ?, ?, 'queued', ?, ?, ?, ?)`, id, requestKey[:], fingerprint[:], m.now().UnixMilli(), legacyAgent, input.Unverified, input.Agent); err != nil {
		return "", ErrUnavailable
	}
	m.start(id, input.Session, 0, input.Run)
	return id, nil
}

func (m *Manager) checkCapacity(session string) error {
	for _, job := range m.active {
		if session != "" && job.session == session {
			return ErrSessionBusy
		}
	}
	if len(m.active) >= MaxConcurrent {
		return ErrAtCapacity
	}
	return nil
}

func newID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(random[:]), nil
}

func (m *Manager) start(id, session string, reviewRounds int, run func(context.Context) (string, error)) {
	// A queued job outlives the request that submitted it.
	jobCtx, cancel := context.WithCancelCause(context.Background())
	job := &activeJob{session: session, reviewRounds: reviewRounds, cancel: cancel, done: make(chan struct{})}
	m.active[id] = job
	m.workers.Add(1)
	go m.execute(jobCtx, id, job, run)
}

func (m *Manager) execute(ctx context.Context, id string, job *activeJob, run func(context.Context) (string, error)) {
	defer m.workers.Done()
	defer close(job.done)
	defer job.cancel(nil)
	var result string
	var runErr error
	m.mu.Lock()
	if ctx.Err() == nil && !m.fault {
		_, runErr = m.store.conn.ExecContext(context.Background(), `UPDATE delegate_jobs SET status='running', started_at=? WHERE id=?`, m.now().UnixMilli(), id)
	}
	canRun := ctx.Err() == nil && runErr == nil && !m.fault
	m.mu.Unlock()

	workCtx, stop := context.WithTimeout(ctx, m.timeout)
	defer stop()
	if canRun {
		result, runErr = run(workCtx)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	defer delete(m.active, id)
	state := Failed
	var saved any
	switch {
	case errors.Is(runErr, ErrStopUnconfirmed):
		// Keep the failed state instead of claiming the cancellation succeeded.
	case errors.Is(context.Cause(workCtx), errInterrupted):
		state = Interrupted
	case errors.Is(context.Cause(workCtx), errCanceled):
		state = Cancelled
	case errors.Is(workCtx.Err(), context.DeadlineExceeded):
		state = TimedOut
	case canRun && runErr == nil:
		state, saved = Succeeded, result
	}
	if err := m.store.finish(context.Background(), id, saved, state, job, m.now()); err != nil {
		m.fault = true
	}
}

func (m *Manager) Get(ctx context.Context, id string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ready(ctx); err != nil {
		return Snapshot{}, err
	}
	return m.get(ctx, id)
}

func (m *Manager) get(ctx context.Context, id string) (Snapshot, error) {
	if len(id) != 32 {
		return Snapshot{}, ErrNotFound
	}
	return readSnapshot(m.store.conn.QueryRowContext(ctx, `SELECT `+snapshotColumns+` FROM delegate_jobs j
		LEFT JOIN review_jobs r ON r.job_id=j.id WHERE j.id=?`, id))
}

func (m *Manager) Cancel(ctx context.Context, id string) (Snapshot, error) {
	m.mu.Lock()
	if err := m.ready(ctx); err != nil {
		m.mu.Unlock()
		return Snapshot{}, err
	}
	job := m.active[id]
	if job == nil {
		snapshot, err := m.get(ctx, id)
		m.mu.Unlock()
		return snapshot, err
	}
	job.cancel(errCanceled)
	m.mu.Unlock()
	select {
	case <-job.done:
		return m.Get(ctx, id)
	case <-ctx.Done():
		return Snapshot{}, ErrCancelWait
	}
}

func (m *Manager) List(ctx context.Context) ([]Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ready(ctx); err != nil {
		return nil, err
	}
	rows, err := m.store.conn.QueryContext(ctx, `SELECT j.id, j.status, j.created_at, j.started_at, j.finished_at, NULL, r.session_id, COALESCE(j.delegation_agent, j.agent), j.unverified
		FROM delegate_jobs j LEFT JOIN review_jobs r ON r.job_id=j.id ORDER BY j.created_at DESC, j.id DESC LIMIT ?`, HistoryLimit)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer rows.Close()
	history := make([]Snapshot, 0)
	for rows.Next() {
		snapshot, err := readSnapshot(rows)
		if err != nil {
			return nil, err
		}
		history = append(history, snapshot)
	}
	if rows.Err() != nil {
		return nil, ErrUnavailable
	}
	return history, nil
}

// ready must be called with mu held.
func (m *Manager) ready(ctx context.Context) error {
	if m.closed || m.fault || m.store.prune(ctx, m.now()) != nil {
		return ErrUnavailable
	}
	return nil
}

func (m *Manager) Close() error {
	m.close.Do(func() {
		m.mu.Lock()
		m.closed = true
		for _, job := range m.active {
			job.cancel(errInterrupted)
		}
		m.mu.Unlock()
		m.workers.Wait()
		if err := m.store.close(); err != nil {
			m.closeErr = ErrUnavailable
		}
	})
	return m.closeErr
}
