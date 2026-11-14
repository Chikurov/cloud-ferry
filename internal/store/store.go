// Package store keeps the app's own state: connected accounts and migration
// jobs. Credentials are not stored here: they live in rclone.conf, which is
// the engine's single source of truth.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Job states.
const (
	StatusQueued      = "queued"
	StatusRunning     = "running"
	StatusVerifying   = "verifying"
	StatusDone        = "done"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
	StatusCanceled    = "canceled"
)

// Account statuses.
const (
	AccountStatusUnknown = "unknown"
	AccountStatusOK      = "ok"
	AccountStatusAuth    = "needs-auth"
)

// Account is a configured cloud remote.
type Account struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Provider   string    `json:"provider"`
	Remote     string    `json:"remote"`
	CreatedAt  time.Time `json:"createdAt"`
	Status     string    `json:"status,omitempty"`
	StatusNote string    `json:"statusNote,omitempty"`
	CheckedAt  time.Time `json:"checkedAt,omitempty"`
}

// Stats mirrors the parts of rclone's core/stats the UI shows.
type Stats struct {
	Bytes          int64   `json:"bytes"`
	TotalBytes     int64   `json:"totalBytes"`
	Speed          float64 `json:"speed"`
	ETA            int64   `json:"eta"`
	Transfers      int64   `json:"transfers"`
	TotalTransfers int64   `json:"totalTransfers"`
	Errors         int64   `json:"errors"`
	ElapsedTime    float64 `json:"elapsedTime"`
}

// CheckResult is a compact summary of operations/check.
type CheckResult struct {
	HashType     string    `json:"hashType"`
	Matched      int       `json:"matched"`
	Differ       int       `json:"differ"`
	MissingOnDst int       `json:"missingOnDst"`
	Broken       int       `json:"broken"`
	CheckedAt    time.Time `json:"checkedAt"`
}

// Job is one migration run.
type Job struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	// Source is the rclone srcFs recorded at start: "<remote>:" for the whole
	// disk and for a multi-folder selection, "<remote>:/<path>" for a legacy
	// single folder.
	Source string `json:"source"`
	// SourcePaths lists the cloud folders the user picked, relative to the
	// cloud root and normalised. When it is not empty, selected folders keep
	// their structure inside Dest and Source is "<remote>:". Empty means the
	// legacy behaviour: copy what Source points at.
	SourcePaths  []string     `json:"sourcePaths,omitempty"`
	Dest         string       `json:"dest"`
	Mode         string       `json:"mode"`
	Status       string       `json:"status"`
	Group        string       `json:"group"`
	RcloneJobID  int64        `json:"rcloneJobId"`
	ExecuteID    string       `json:"executeId,omitempty"`
	Error        string       `json:"error,omitempty"`
	ErrorTitle   string       `json:"errorTitle,omitempty"`
	ErrorAdvice  string       `json:"errorAdvice,omitempty"`
	Stats        Stats        `json:"stats"`
	Check        *CheckResult `json:"check,omitempty"`
	Failures     []string     `json:"failures,omitempty"`
	FailureCount int          `json:"failureCount,omitempty"`
	CreatedAt    time.Time    `json:"createdAt"`
	StartedAt    time.Time    `json:"startedAt"`
	FinishedAt   time.Time    `json:"finishedAt"`
}

type state struct {
	Accounts []Account `json:"accounts"`
	Jobs     []Job     `json:"jobs"`
}

// Store is a tiny JSON-file store guarded by a mutex. It is intentionally
// boring: the workload is a handful of records, and a single writer keeps the
// file consistent.
type Store struct {
	mu   sync.RWMutex
	path string
	st   state
}

// Open loads (or creates) the state file inside dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	s := &Store{path: filepath.Join(dir, "state.json")}
	if err := s.load(s.path); err == nil {
		return s, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		// A corrupt state must not brick the app: fall back to the previous
		// good copy, then to an empty state.
		log.Printf("store: state is unreadable (%v), trying backup", err)
		if backupErr := s.load(s.path + ".bak"); backupErr == nil {
			return s, nil
		}
		log.Printf("store: backup is unreadable too, starting empty")
		s.st = state{}
		return s, nil
	}
	return s, nil
}

func (s *Store) load(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, &s.st)
}

// Snapshot returns a copy of the whole state.
func (s *Store) Snapshot() ([]Account, []Job) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	accounts := make([]Account, len(s.st.Accounts))
	copy(accounts, s.st.Accounts)
	jobs := make([]Job, len(s.st.Jobs))
	copy(jobs, s.st.Jobs)
	return accounts, jobs
}

// Accounts returns all accounts sorted by creation time.
func (s *Store) Accounts() []Account {
	accounts, _ := s.Snapshot()
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].CreatedAt.Before(accounts[j].CreatedAt) })
	return accounts
}

// Account returns one account by id.
func (s *Store) Account(id string) (Account, bool) {
	accounts, _ := s.Snapshot()
	for _, a := range accounts {
		if a.ID == id {
			return a, true
		}
	}
	return Account{}, false
}

// AddAccount stores a new account.
func (s *Store) AddAccount(a Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.st.Accounts = append(s.st.Accounts, a)
	return s.saveLocked()
}

// UpdateAccount mutates one account and persists the change.
func (s *Store) UpdateAccount(id string, fn func(*Account) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.st.Accounts {
		if s.st.Accounts[i].ID != id {
			continue
		}
		if !fn(&s.st.Accounts[i]) {
			return nil
		}
		return s.saveLocked()
	}
	return os.ErrNotExist
}

// DeleteAccount removes an account and returns whether it existed.
func (s *Store) DeleteAccount(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, a := range s.st.Accounts {
		if a.ID == id {
			s.st.Accounts = append(s.st.Accounts[:i], s.st.Accounts[i+1:]...)
			return true, s.saveLocked()
		}
	}
	return false, nil
}

// AddJob stores a new job.
func (s *Store) AddJob(j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.st.Jobs = append(s.st.Jobs, j)
	s.pruneLocked()
	return s.saveLocked()
}

// maxJobs bounds the stored history so the state file and the API response do
// not grow forever. Active jobs are always kept.
const maxJobs = 100

func (s *Store) pruneLocked() {
	if len(s.st.Jobs) <= maxJobs {
		return
	}
	finished := make([]Job, 0, len(s.st.Jobs))
	active := make([]Job, 0, len(s.st.Jobs))
	for _, job := range s.st.Jobs {
		switch job.Status {
		case StatusRunning, StatusQueued, StatusVerifying:
			active = append(active, job)
		default:
			finished = append(finished, job)
		}
	}
	sort.Slice(finished, func(a, b int) bool { return finished[a].CreatedAt.After(finished[b].CreatedAt) })
	keep := maxJobs - len(active)
	if keep < 0 {
		keep = 0
	}
	if len(finished) > keep {
		finished = finished[:keep]
	}
	s.st.Jobs = append(active, finished...)
}

// Job returns one job by id.
func (s *Store) Job(id string) (Job, bool) {
	jobs := s.Jobs()
	for _, j := range jobs {
		if j.ID == id {
			return j, true
		}
	}
	return Job{}, false
}

// Jobs returns all jobs, newest first.
func (s *Store) Jobs() []Job {
	_, jobs := s.Snapshot()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return jobs
}

// UpdateJob applies fn to the job with id and persists the result when fn
// reports a change.
func (s *Store) UpdateJob(id string, fn func(*Job) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.st.Jobs {
		if s.st.Jobs[i].ID != id {
			continue
		}
		if !fn(&s.st.Jobs[i]) {
			return nil
		}
		return s.saveLocked()
	}
	return os.ErrNotExist
}

func (s *Store) saveLocked() error {
	payload, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmp := s.path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		file.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	if _, err := os.Stat(s.path); err == nil {
		if err := copyFile(s.path, s.path+".bak"); err != nil {
			log.Printf("store: backup: %v", err)
		}
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600)
}
