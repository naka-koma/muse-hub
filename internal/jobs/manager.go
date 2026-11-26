package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	MaxConcurrent = 5
	Retention     = time.Hour
)

var ErrNotFound = errors.New("job_not_found")
var ErrAtCapacity = errors.New("job_capacity_reached")

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
)

type Result struct {
	Message     string    `json:"message"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type Snapshot struct {
	JobID  string  `json:"job_id"`
	Status Status  `json:"status"`
	Result *Result `json:"result,omitempty"`
}

type job struct {
	snapshot Snapshot
	created  time.Time
}

type Manager struct {
	mu   sync.Mutex
	jobs map[string]*job
	now  func() time.Time
}

func NewManager() *Manager {
	return &Manager{jobs: make(map[string]*job), now: time.Now}
}

func (m *Manager) Start(delay time.Duration) (string, error) {
	if delay < time.Second || delay > time.Minute {
		return "", fmt.Errorf("delay_seconds must be an integer from 1 to 60")
	}
	m.mu.Lock()
	m.cleanupLocked()
	active := 0
	for _, j := range m.jobs {
		if j.snapshot.Status == Queued || j.snapshot.Status == Running {
			active++
		}
	}
	if active >= MaxConcurrent {
		m.mu.Unlock()
		return "", ErrAtCapacity
	}
	id, err := randomID()
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("generate job id: %w", err)
	}
	now := m.now()
	j := &job{snapshot: Snapshot{JobID: id, Status: Queued}, created: now}
	m.jobs[id] = j
	m.mu.Unlock()
	go m.run(id, delay)
	return id, nil
}

func (m *Manager) Get(id string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked()
	j, ok := m.jobs[id]
	if !ok {
		return Snapshot{}, ErrNotFound
	}
	return j.snapshot, nil
}

func (m *Manager) run(id string, delay time.Duration) {
	started := m.now()
	m.mu.Lock()
	if j := m.jobs[id]; j != nil {
		j.snapshot.Status = Running
	}
	m.mu.Unlock()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	<-timer.C
	completed := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if j := m.jobs[id]; j != nil {
		j.snapshot.Status = Succeeded
		j.snapshot.Result = &Result{
			Message:     "dummy job completed",
			StartedAt:   started.UTC(),
			CompletedAt: completed.UTC(),
		}
		j.created = completed
	}
}

func (m *Manager) cleanupLocked() {
	cutoff := m.now().Add(-Retention)
	for id, j := range m.jobs {
		if j.snapshot.Status == Succeeded && j.created.Before(cutoff) {
			delete(m.jobs, id)
		}
	}
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
