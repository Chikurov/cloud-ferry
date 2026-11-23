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
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	// Login is the provider login shown to the user, e.g. the WebDAV user
	// name. It is empty for providers that expose no login and is simply
	// absent in state files written by older versions.
	Login      string    `json:"login,omitempty"`
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
	// IdempotencyKey is the client key that created this job; empty when the
	// request had none. A retry with the same key returns this job instead of
	// starting a second transfer. The field is absent in state files written
	// by older builds and is not written while empty.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	// Source is the rclone srcFs recorded at start: "<remote>:" for the whole
	// disk and for a multi-folder selection, "<remote>:/<path>" for a legacy
	// single folder.
	Source string `json:"source"`
	// SourcePaths lists the cloud folders the user picked, relative to the
	// cloud root and normalised. When it is not empty, selected folders keep
	// their structure inside Dest and Source is "<remote>:". Empty means the
	// legacy behaviour: copy what Source points at.
	SourcePaths []string `json:"sourcePaths,omitempty"`
	// SourceFiles lists individual cloud files the user picked, relative to
	// the cloud root and normalised. Together with SourcePaths it makes Source
	// "<remote>:"; the selected files keep their structure inside Dest.
	SourceFiles     []string `json:"sourceFiles,omitempty"`
	ExcludedFolders []string `json:"excludedFolders,omitempty"`
	Dest            string   `json:"dest"`
	InitialDest     string   `json:"initialDest,omitempty"`
	Mode            string   `json:"mode"`
	Status          string   `json:"status"`
	Group           string   `json:"group"`
	RcloneJobID     int64    `json:"rcloneJobId"`
	ExecuteID       string   `json:"executeId,omitempty"`
	Error           string   `json:"error,omitempty"`
	ErrorTitle      string   `json:"errorTitle,omitempty"`
	ErrorAdvice     string   `json:"errorAdvice,omitempty"`
	// BytesBase is how much this job had already transferred when the current
	// attempt was resumed. rclone counts the stats of one engine job, so a
	// resumed attempt reports only the remainder; BytesBase keeps the stored
	// progress cumulative instead of letting it jump back.
	BytesBase    int64        `json:"bytesBase,omitempty"`
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
			// Persist the recovered state right away, so the next start does
			// not read the corrupt file again. saveLocked refuses to copy the
			// unreadable primary file into the backup, so the good .bak stays
			// intact.
			if saveErr := s.saveLocked(s.st); saveErr != nil {
				log.Printf("store: save recovered state: %v", saveErr)
			}
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

// Snapshot returns a copy of the whole state. Slices and pointers are copied
// too, so a caller cannot mutate the live store through the returned value.
func (s *Store) Snapshot() ([]Account, []Job) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	accounts := make([]Account, len(s.st.Accounts))
	copy(accounts, s.st.Accounts)
	jobs := make([]Job, len(s.st.Jobs))
	for i := range s.st.Jobs {
		jobs[i] = cloneJob(s.st.Jobs[i])
	}
	return accounts, jobs
}

// cloneJob copies the reference fields of a job so the result shares no
// mutable memory with the stored record.
func cloneJob(j Job) Job {
	if j.SourcePaths != nil {
		j.SourcePaths = append([]string(nil), j.SourcePaths...)
	}
	j.ExcludedFolders = append([]string(nil), j.ExcludedFolders...)
	if j.SourceFiles != nil {
		j.SourceFiles = append([]string(nil), j.SourceFiles...)
	}
	if j.Failures != nil {
		j.Failures = append([]string(nil), j.Failures...)
	}
	if j.Check != nil {
		check := *j.Check
		j.Check = &check
	}
	return j
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

// AddAccount stores a new account. The change is built as a new state, saved,
// and only then committed to memory: a failed write leaves the store unchanged.
func (s *Store) AddAccount(a Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	accounts := make([]Account, len(s.st.Accounts)+1)
	copy(accounts, s.st.Accounts)
	accounts[len(s.st.Accounts)] = a
	next := state{Accounts: accounts, Jobs: s.st.Jobs}
	if err := s.saveLocked(next); err != nil {
		return err
	}
	s.st = next
	return nil
}

// UpdateAccount mutates one account and persists the change. fn works on a
// copy: when the save fails, the in-memory account keeps its old value.
func (s *Store) UpdateAccount(id string, fn func(*Account) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.st.Accounts {
		if s.st.Accounts[i].ID != id {
			continue
		}
		updated := s.st.Accounts[i]
		if !fn(&updated) {
			return nil
		}
		accounts := make([]Account, len(s.st.Accounts))
		copy(accounts, s.st.Accounts)
		accounts[i] = updated
		next := state{Accounts: accounts, Jobs: s.st.Jobs}
		if err := s.saveLocked(next); err != nil {
			return err
		}
		s.st = next
		return nil
	}
	return os.ErrNotExist
}

// DeleteAccount removes an account and returns whether it existed.
func (s *Store) DeleteAccount(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, a := range s.st.Accounts {
		if a.ID != id {
			continue
		}
		accounts := make([]Account, 0, len(s.st.Accounts)-1)
		accounts = append(accounts, s.st.Accounts[:i]...)
		accounts = append(accounts, s.st.Accounts[i+1:]...)
		next := state{Accounts: accounts, Jobs: s.st.Jobs}
		if err := s.saveLocked(next); err != nil {
			return true, err
		}
		s.st = next
		return true, nil
	}
	return false, nil
}

// AddJob stores a new job.
func (s *Store) AddJob(j Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs := make([]Job, 0, len(s.st.Jobs)+1)
	jobs = append(jobs, s.st.Jobs...)
	jobs = append(jobs, j)
	jobs = pruneJobs(jobs)
	next := state{Accounts: s.st.Accounts, Jobs: jobs}
	if err := s.saveLocked(next); err != nil {
		return err
	}
	s.st = next
	return nil
}

// maxJobs bounds the stored history so the state file and the API response do
// not grow forever. Active jobs and jobs whose idempotency key is still valid
// are always kept; the state can therefore briefly exceed maxJobs, but a key
// that would still replay can never be silently forgotten.
const maxJobs = 100

// IdempotencyWindow is how long a client idempotency key stays meaningful. The
// job manager replays keys within it and pruning keeps such a job alive for the
// same time, so both must use one value.
const IdempotencyWindow = 24 * time.Hour

// pruneJobs bounds the job history without touching the input slice. A job
// with an unexpired idempotency key survives even when it is the oldest record:
// evicting it would make a retry with the same key start a second transfer.
func pruneJobs(jobs []Job) []Job {
	if len(jobs) <= maxJobs {
		return jobs
	}
	now := time.Now()
	active := make([]Job, 0, len(jobs))
	protected := make([]Job, 0, len(jobs))
	finished := make([]Job, 0, len(jobs))
	for _, job := range jobs {
		switch {
		case job.Status == StatusRunning || job.Status == StatusQueued || job.Status == StatusVerifying:
			active = append(active, job)
		case job.IdempotencyKey != "" && now.Sub(job.CreatedAt) <= IdempotencyWindow:
			protected = append(protected, job)
		default:
			finished = append(finished, job)
		}
	}
	sort.Slice(finished, func(a, b int) bool { return finished[a].CreatedAt.After(finished[b].CreatedAt) })
	keep := maxJobs - len(active) - len(protected)
	if keep < 0 {
		keep = 0
	}
	if len(finished) > keep {
		finished = finished[:keep]
	}
	return append(append(active, protected...), finished...)
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
// reports a change. fn works on a deep copy: a failed save leaves the stored
// job untouched.
func (s *Store) UpdateJob(id string, fn func(*Job) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.st.Jobs {
		if s.st.Jobs[i].ID != id {
			continue
		}
		updated := cloneJob(s.st.Jobs[i])
		if !fn(&updated) {
			return nil
		}
		jobs := make([]Job, len(s.st.Jobs))
		copy(jobs, s.st.Jobs)
		jobs[i] = updated
		next := state{Accounts: s.st.Accounts, Jobs: jobs}
		if err := s.saveLocked(next); err != nil {
			return err
		}
		s.st = next
		return nil
	}
	return os.ErrNotExist
}

// saveLocked persists st atomically. The caller commits st to memory only when
// this returns nil.
func (s *Store) saveLocked(st state) error {
	payload, err := json.MarshalIndent(st, "", "  ")
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
		os.Remove(tmp)
		return fmt.Errorf("write state: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(tmp)
		return fmt.Errorf("sync state: %w", err)
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close state: %w", err)
	}
	if _, err := os.Stat(s.path); err == nil {
		if err := backupFile(s.path, s.path+".bak"); err != nil {
			log.Printf("store: backup: %v", err)
		}
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace state: %w", err)
	}
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil
}

// backupFile keeps a copy of the current state, but only when it is readable
// JSON. After recovery the primary file is corrupt; copying it would destroy
// the last good backup.
func backupFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if len(data) > 0 && !json.Valid(data) {
		return errors.New("current state is unreadable, keeping the previous backup")
	}
	return os.WriteFile(to, data, 0o600)
}
