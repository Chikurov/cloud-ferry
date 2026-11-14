// Package jobs turns rclone's async operations into persisted, pollable jobs.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"cloudferry/internal/disk"
	"cloudferry/internal/humanize"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

// maxStoredFailures caps the per-file error list kept in the state file; the
// total count is still reported.
const maxStoredFailures = 200

const maxStatusFailures = 3

// ErrConflict means the job is in a state that does not allow the action.
var ErrConflict = errors.New("job state conflict")

// ErrInvalidMode means the caller asked for an unknown transfer mode.
var ErrInvalidMode = errors.New("unknown transfer mode")

// ErrSourceNotFound means a selected cloud folder does not exist or is not a
// directory. It is a user mistake, not an outage, so the HTTP layer answers 400.
var ErrSourceNotFound = errors.New("cloud source not found")

// SourceNotFoundError names the folder that failed verification.
type SourceNotFoundError struct {
	Path string
}

func (e *SourceNotFoundError) Error() string { return "cloud source not found: " + e.Path }

func (e *SourceNotFoundError) Unwrap() error { return ErrSourceNotFound }

// Source is the cloud side of a job. Path is one legacy folder whose contents
// go straight into Dest. Paths is the multi-folder selection whose structure is
// preserved inside Dest. Paths wins when both are set.
type Source struct {
	Path  string
	Paths []string
}

// selectedPaths returns the cloud folders that need verification.
func selectedPaths(src Source) []string {
	if len(src.Paths) > 0 {
		return src.Paths
	}
	if src.Path != "" {
		return []string{strings.TrimPrefix(src.Path, "/")}
	}
	return nil
}

// includeEscaper lists the rclone glob metacharacters that must be escaped in
// a literal folder name. {@code "{"} and {@code "}"} are included because
// rclone treats them as pattern delimiters.
const includeEscaper = `\*?[]{}`

// escapeIncludeRule escapes a literal path for use inside an rclone include
// rule. Only the reserved glob characters are escaped: an escaped path stays a
// literal, so a folder named "a*b" selects exactly that folder.
func escapeIncludeRule(p string) string {
	var b strings.Builder
	b.Grow(len(p) + 8)
	for _, r := range p {
		if strings.ContainsRune(includeEscaper, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// IncludeRules returns the rclone include rules that select every path and its
// subtree. A leading slash makes rclone emit directory rules for the ancestors,
// so it prunes unrelated folders instead of walking the whole disk.
func IncludeRules(paths []string) []string {
	rules := make([]string, 0, len(paths))
	for _, p := range paths {
		rules = append(rules, "/"+escapeIncludeRule(p)+"/**")
	}
	return rules
}

// SourceFS returns the rclone srcFs for a source and, for a multi-folder
// selection, the _filter block that restricts the transfer to it.
func SourceFS(remote string, src Source) (string, map[string]any) {
	if len(src.Paths) > 0 {
		return remote + ":", map[string]any{"IncludeRule": IncludeRules(src.Paths)}
	}
	if src.Path != "" {
		return remote + ":/" + strings.TrimPrefix(src.Path, "/"), nil
	}
	return remote + ":", nil
}

// VerifySources checks that every folder exists in the cloud and is a
// directory before a transfer starts. A missing folder is ErrSourceNotFound
// (and names the folder); anything else is an rclone failure the caller must
// report as an outage rather than a bad request.
func VerifySources(ctx context.Context, caller Caller, remote string, paths []string) error {
	for _, p := range paths {
		var out struct {
			Item *struct {
				IsDir bool `json:"IsDir"`
			} `json:"item"`
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := caller.Call(callCtx, "operations/stat", map[string]any{"fs": remote + ":", "remote": p}, &out)
		cancel()
		if err != nil {
			if isMissingSource(err) {
				return &SourceNotFoundError{Path: p}
			}
			return fmt.Errorf("verify %s: %w", p, err)
		}
		if out.Item == nil || !out.Item.IsDir {
			return &SourceNotFoundError{Path: p}
		}
	}
	return nil
}

// isMissingSource reports whether an rclone error means "there is no such
// folder", as opposed to an auth or network failure.
func isMissingSource(err error) bool {
	var apiErr *rc.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Status == http.StatusNotFound {
		return true
	}
	body := strings.ToLower(apiErr.Body)
	for _, marker := range []string{"not found", "does not exist", "no such file"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// sourceForJob reconstructs the cloud source of a stored job, including jobs
// written by older builds that only have Source.
func sourceForJob(job store.Job) Source {
	if len(job.SourcePaths) > 0 {
		return Source{Paths: append([]string(nil), job.SourcePaths...)}
	}
	_, rest, ok := strings.Cut(job.Source, ":")
	if !ok {
		return Source{}
	}
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return Source{}
	}
	return Source{Path: rest}
}

// remoteOf returns the rclone remote name ("cf_abc") of a recorded srcFs.
func remoteOf(source string) string {
	if i := strings.Index(source, ":"); i > 0 {
		return source[:i]
	}
	return source
}

// destOverlapsActive reports whether a new destination and an active one are
// the same folder or one contains the other. The nesting is checked in both
// directions: a transfer into a parent tree also writes over the active one,
// so a sync that deletes extra files can destroy it.
func destOverlapsActive(dst, active string) bool {
	dst, active = path.Clean(dst), path.Clean(active)
	if dst == active {
		return true
	}
	return strings.HasPrefix(dst, active+"/") || strings.HasPrefix(active, dst+"/")
}

// Caller is the subset of the rclone client the manager needs.
type Caller interface {
	Call(ctx context.Context, method string, in, out any) error
}

// Options tunes the transfer defaults passed to rclone.
type Options struct {
	Transfers int
	Checkers  int
	TpsLimit  int
	PollEvery time.Duration
}

func (o Options) withDefaults() Options {
	if o.Transfers <= 0 {
		o.Transfers = 8
	}
	if o.Checkers <= 0 {
		o.Checkers = 16
	}
	if o.TpsLimit <= 0 {
		o.TpsLimit = 20
	}
	if o.PollEvery <= 0 {
		o.PollEvery = 2 * time.Second
	}
	return o
}

// Manager starts jobs, polls rclone and keeps store.Job records up to date.
type Manager struct {
	store *store.Store
	rc    Caller
	opts  Options

	createMu     sync.Mutex
	mu           sync.Mutex
	statusErrors map[string]int
}

// NewManager wires a manager. Call Recover once at startup.
func NewManager(st *store.Store, client Caller, opts Options) *Manager {
	return &Manager{store: st, rc: client, opts: opts.withDefaults(), statusErrors: map[string]int{}}
}

// Recover reconciles jobs that were running when the process died. A job whose
// rclone counterpart still runs is reattached; a job that finished while the
// app was down is finalised with its real result; everything else is marked
// interrupted so the user can continue it.
func (m *Manager) Recover() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, job := range m.store.Jobs() {
		switch job.Status {
		case store.StatusQueued:
			if err := m.store.UpdateJob(job.ID, func(j *store.Job) bool {
				if j.Status != store.StatusQueued {
					return false
				}
				j.Status = store.StatusInterrupted
				j.Error = "Приложение перезапустилось до начала скачивания."
				j.ErrorTitle = "Скачивание прервано"
				j.ErrorAdvice = "Нажмите «Продолжить», чтобы начать заново."
				return true
			}); err != nil {
				return err
			}
		case store.StatusRunning:
			status, err := m.statusOf(ctx, job)
			switch {
			case errors.Is(err, errNoRcloneJob):
				m.markInterrupted(job.ID, "Приложение перезапустилось во время скачивания.")
			case rc.IsUnavailable(err):
				// rclone is not reachable yet, so the transfer may still be
				// running. Keep the record and let Poll reconcile it: the
				// engine socket may simply not be ready at startup.
				log.Printf("jobs: recover %s: rclone is unavailable (%v), leaving it running", job.ID, err)
			case err != nil:
				m.markInterrupted(job.ID, "Приложение перезапустилось во время скачивания.")
			case status.Finished:
				// rclone finished while the app was down: write the real
				// result instead of pretending the transfer was interrupted.
				m.finalize(job, status)
			case !sameTransfer(job, status):
				m.markInterrupted(job.ID, "Приложение перезапустилось во время скачивания.")
			}
		}
	}
	return nil
}

// rcloneStatus is the subset of rclone's job/status the manager needs.
type rcloneStatus struct {
	Finished  bool            `json:"finished"`
	Success   bool            `json:"success"`
	Error     string          `json:"error"`
	ExecuteID string          `json:"executeId"`
	Output    json.RawMessage `json:"output"`
}

// errNoRcloneJob marks a stored job that has no engine job behind it.
var errNoRcloneJob = errors.New("job has no rclone job id")

// statusOf asks rclone about the engine job recorded on job.
func (m *Manager) statusOf(ctx context.Context, job store.Job) (rcloneStatus, error) {
	if job.RcloneJobID == 0 {
		return rcloneStatus{}, errNoRcloneJob
	}
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var status rcloneStatus
	err := m.rc.Call(callCtx, "job/status", map[string]any{"jobid": job.RcloneJobID}, &status)
	return status, err
}

// sameTransfer reports whether an engine status answer belongs to job's own
// transfer rather than to a reused job id from a restarted rclone.
func sameTransfer(job store.Job, status rcloneStatus) bool {
	return job.ExecuteID == "" || status.ExecuteID == "" || status.ExecuteID == job.ExecuteID
}

// reattach reports whether the rclone job behind job is still alive and
// belongs to this record (same executeId).
func (m *Manager) reattach(ctx context.Context, job store.Job) bool {
	status, err := m.statusOf(ctx, job)
	return err == nil && !status.Finished && sameTransfer(job, status)
}

// claimDest reserves a destination for one transfer. It reports ErrConflict
// when another queued or running job already writes into an overlapping tree.
// Reserve runs while createMu is held, so a concurrent Start or Resume cannot
// pass the same check: the winner is persisted before the loser looks.
func (m *Manager) claimDest(dst string, reserve func() error) error {
	m.createMu.Lock()
	defer m.createMu.Unlock()

	for _, existing := range m.store.Jobs() {
		if (existing.Status == store.StatusRunning || existing.Status == store.StatusQueued) && destOverlapsActive(dst, existing.Dest) {
			return ErrConflict
		}
	}
	return reserve()
}

// Start records the job before launching rclone: if the process dies between
// the two steps, Recover can still see it. Creation is serialized so two
// requests can never start transfers into the same tree. Selected cloud folders
// are verified before anything is persisted or launched.
func (m *Manager) Start(ctx context.Context, account store.Account, src Source, dstPath, mode string) (store.Job, error) {
	if mode != "copy" && mode != "sync" {
		return store.Job{}, ErrInvalidMode
	}
	if err := VerifySources(ctx, m.rc, account.Remote, selectedPaths(src)); err != nil {
		return store.Job{}, err
	}

	id, err := newID()
	if err != nil {
		return store.Job{}, err
	}
	source, _ := SourceFS(account.Remote, src)

	job := store.Job{
		ID:        id,
		AccountID: account.ID,
		Source:    source,
		Dest:      dstPath,
		Mode:      mode,
		Status:    store.StatusQueued,
		Group:     newGroup(id),
		CreatedAt: time.Now().UTC(),
	}
	if len(src.Paths) > 0 {
		job.SourcePaths = append([]string(nil), src.Paths...)
	}

	if err := m.claimDest(dstPath, func() error { return m.store.AddJob(job) }); err != nil {
		return store.Job{}, err
	}

	rcloneJobID, executeID, err := m.launch(ctx, job)
	if err != nil {
		m.markFailed(job.ID, err)
		return job, err
	}
	if err := m.markRunning(job.ID, rcloneJobID, executeID); err != nil {
		// The job is no longer ours: stop the orphan so two transfers do not
		// write into the same folder.
		m.stopRclone(context.Background(), rcloneJobID)
		return store.Job{}, err
	}

	updated, _ := m.store.Job(job.ID)
	return updated, nil
}

// Resume continues an interrupted or failed job. It refuses to touch a job that
// is already running: that would create a second transfer into the same folder.
func (m *Manager) Resume(ctx context.Context, id string) (store.Job, error) {
	job, ok := m.store.Job(id)
	if !ok {
		return store.Job{}, fmt.Errorf("job %s not found", id)
	}
	if job.Status != store.StatusFailed && job.Status != store.StatusInterrupted && job.Status != store.StatusCanceled {
		return store.Job{}, ErrConflict
	}
	// A folder may have disappeared while the job was paused. Verify before the
	// job is claimed again: a sync into a vanished folder would empty its
	// destination.
	if paths := selectedPaths(sourceForJob(job)); len(paths) > 0 {
		remote := remoteOf(job.Source)
		if remote == "" {
			return store.Job{}, fmt.Errorf("job %s has no cloud source", id)
		}
		if err := VerifySources(ctx, m.rc, remote, paths); err != nil {
			return store.Job{}, err
		}
	}
	// The previous record may point at an rclone job that is still running:
	// starting a second transfer into the same folder would corrupt it.
	if m.reattach(ctx, job) {
		return store.Job{}, ErrConflict
	}

	group := newGroup(id)
	startedAt := time.Now().UTC()
	claimed := false
	err := m.claimDest(job.Dest, func() error {
		if err := m.store.UpdateJob(id, func(j *store.Job) bool {
			if j.Status != store.StatusFailed && j.Status != store.StatusInterrupted && j.Status != store.StatusCanceled {
				return false
			}
			claimed = true
			j.Status = store.StatusQueued
			j.Group = group
			j.RcloneJobID = 0
			j.ExecuteID = ""
			j.Error = ""
			j.ErrorTitle = ""
			j.ErrorAdvice = ""
			j.Failures = nil
			j.FailureCount = 0
			j.Check = nil
			j.StartedAt = startedAt
			j.FinishedAt = time.Time{}
			j.Stats = store.Stats{}
			return true
		}); err != nil {
			return err
		}
		if !claimed {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return store.Job{}, err
	}

	job.Group = group
	rcloneJobID, executeID, err := m.launch(ctx, job)
	if err != nil {
		m.markFailed(id, err)
		return store.Job{}, err
	}
	if err := m.markRunning(id, rcloneJobID, executeID); err != nil {
		m.stopRclone(context.Background(), rcloneJobID)
		return store.Job{}, err
	}
	updated, _ := m.store.Job(id)
	return updated, nil
}

// Stop cancels a job. It only affects a transfer that is queued or running and
// never rewrites a finished result.
func (m *Manager) Stop(ctx context.Context, id string) (string, error) {
	job, ok := m.store.Job(id)
	if !ok {
		return "", fmt.Errorf("job %s not found", id)
	}
	if job.Status != store.StatusRunning && job.Status != store.StatusQueued {
		return "", ErrConflict
	}

	var warning string
	if job.Status == store.StatusRunning && job.RcloneJobID > 0 {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := m.rc.Call(callCtx, "job/stop", map[string]any{"jobid": job.RcloneJobID}, nil); err != nil {
			warning = "Скачивание приостановлено, но приложение не получило подтверждение. Если прогресс продолжится, перезапустите Cloud Ferry."
		}
	}
	m.resetStatusError(id)

	err := m.store.UpdateJob(id, func(j *store.Job) bool {
		if j.Status != store.StatusRunning && j.Status != store.StatusQueued {
			return false
		}
		j.Status = store.StatusCanceled
		j.Error = ""
		j.ErrorTitle = ""
		j.ErrorAdvice = ""
		j.FinishedAt = time.Now().UTC()
		return true
	})
	if err != nil {
		return warning, err
	}
	return warning, nil
}

func (m *Manager) launch(ctx context.Context, job store.Job) (int64, string, error) {
	method := "sync/copy"
	if job.Mode == "sync" {
		method = "sync/sync"
	}

	config := map[string]any{
		"Transfers": m.opts.Transfers,
		"Checkers":  m.opts.Checkers,
		"TpsLimit":  m.opts.TpsLimit,
	}
	// Leave a tenth of the destination free: rclone stops gracefully when the
	// limit is reached instead of failing every remaining file with ENOSPC.
	if limit, ok := transferLimit(job.Dest); ok {
		config["MaxTransfer"] = limit
	}

	req := map[string]any{
		"dstFs":              job.Dest,
		"_async":             true,
		"_group":             job.Group,
		"_config":            config,
		"createEmptySrcDirs": true,
		// Ask rclone to return the per-file error list in the job output.
		"error": true,
	}
	srcFs, filter := SourceFS(remoteOf(job.Source), sourceForJob(job))
	req["srcFs"] = srcFs
	if filter != nil {
		req["_filter"] = filter
	}

	var out struct {
		JobID     int64  `json:"jobid"`
		ExecuteID string `json:"executeId"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := m.rc.Call(callCtx, method, req, &out); err != nil {
		return 0, "", err
	}
	if out.JobID == 0 {
		return 0, "", fmt.Errorf("%s did not return a job id", method)
	}
	return out.JobID, out.ExecuteID, nil
}

func (m *Manager) markRunning(id string, rcloneJobID int64, executeID string) error {
	err := m.store.UpdateJob(id, func(j *store.Job) bool {
		if j.Status != store.StatusQueued {
			return false
		}
		j.Status = store.StatusRunning
		j.RcloneJobID = rcloneJobID
		j.ExecuteID = executeID
		return true
	})
	if err != nil {
		return err
	}
	current, ok := m.store.Job(id)
	if !ok || current.Status != store.StatusRunning || current.RcloneJobID != rcloneJobID {
		return ErrConflict
	}
	return nil
}

func (m *Manager) markFailed(id string, cause error) {
	if err := m.store.UpdateJob(id, func(j *store.Job) bool {
		j.Status = store.StatusFailed
		j.Error = cause.Error()
		applyHint(j)
		j.FinishedAt = time.Now().UTC()
		return true
	}); err != nil {
		log.Printf("jobs: mark %s failed: %v", id, err)
	}
}

func (m *Manager) stopRclone(ctx context.Context, jobID int64) {
	if jobID == 0 {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := m.rc.Call(callCtx, "job/stop", map[string]any{"jobid": jobID}, nil); err != nil {
		log.Printf("jobs: stop orphan %d: %v", jobID, err)
	}
}

// Poll refreshes every running job once.
func (m *Manager) Poll(ctx context.Context) {
	for _, job := range m.store.Jobs() {
		if job.Status != store.StatusRunning {
			continue
		}
		m.pollOne(ctx, job)
	}
}

func (m *Manager) pollOne(ctx context.Context, job store.Job) {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var stats struct {
		Bytes          int64   `json:"bytes"`
		TotalBytes     int64   `json:"totalBytes"`
		Speed          float64 `json:"speed"`
		ETA            int64   `json:"eta"`
		Transfers      int64   `json:"transfers"`
		TotalTransfers int64   `json:"totalTransfers"`
		Errors         int64   `json:"errors"`
		ElapsedTime    float64 `json:"elapsedTime"`
	}
	if err := m.rc.Call(callCtx, "core/stats", map[string]any{"group": job.Group}, &stats); err == nil {
		if updateErr := m.store.UpdateJob(job.ID, func(j *store.Job) bool {
			if j.Status != store.StatusRunning || j.RcloneJobID != job.RcloneJobID {
				return false
			}
			j.Stats = store.Stats{
				Bytes: stats.Bytes, TotalBytes: stats.TotalBytes, Speed: stats.Speed,
				ETA: stats.ETA, Transfers: stats.Transfers, TotalTransfers: stats.TotalTransfers,
				Errors: stats.Errors, ElapsedTime: stats.ElapsedTime,
			}
			return true
		}); updateErr != nil {
			log.Printf("jobs: save stats for %s: %v", job.ID, updateErr)
		}
	}

	// A stored job without an engine job cannot be followed: it is already
	// gone, so there is nothing to stop and nothing to wait for.
	if job.RcloneJobID == 0 {
		m.markInterrupted(job.ID, "Скачивание прервалось: приложение перезапустилось.")
		return
	}

	status, err := m.statusOf(ctx, job)
	if err != nil {
		if rc.IsUnavailable(err) {
			// rclone itself is unreachable, but the transfer may still be
			// writing files. A transport failure is not a lost transfer: keep
			// the job running and retry on the next poll.
			log.Printf("jobs: poll %s: rclone is unavailable: %v", job.ID, err)
			return
		}
		if failures := m.bumpStatusError(job.ID); failures >= maxStatusFailures {
			m.resetStatusError(job.ID)
			// Stop the engine job before marking the record interrupted: if it
			// is still transferring, a later resume would start a second copy
			// into the same tree.
			m.stopRclone(ctx, job.RcloneJobID)
			m.markInterrupted(job.ID, "Скачивание прервалось: приложение перезапустилось.")
		}
		return
	}
	m.resetStatusError(job.ID)

	// A re-used job id from a restarted rclone belongs to a different transfer.
	if !sameTransfer(job, status) {
		m.markInterrupted(job.ID, "Скачивание прервалось: приложение перезапустилось.")
		return
	}
	if !status.Finished {
		return
	}
	m.finalize(job, status)
}

// finalize writes the terminal state of a job from the engine status answer.
// Poll and Recover share it, so a transfer that finished while the app was down
// lands in the same state as one that finished while it was running.
func (m *Manager) finalize(job store.Job, status rcloneStatus) {
	var report struct {
		Error []string `json:"error"`
	}
	if len(status.Output) > 0 {
		if err := json.Unmarshal(status.Output, &report); err != nil {
			log.Printf("jobs: decode output for %s: %v", job.ID, err)
		}
	}

	if err := m.store.UpdateJob(job.ID, func(j *store.Job) bool {
		if j.Status != store.StatusRunning || j.RcloneJobID != job.RcloneJobID {
			return false
		}
		j.FailureCount = len(report.Error)
		if len(report.Error) > maxStoredFailures {
			j.Failures = append([]string(nil), report.Error[:maxStoredFailures]...)
		} else {
			j.Failures = append([]string(nil), report.Error...)
		}
		j.FinishedAt = time.Now().UTC()
		if status.Error != "" || !status.Success {
			j.Status = store.StatusFailed
			j.Error = status.Error
			if j.Error == "" {
				j.Error = "Часть файлов не скачалась."
			}
			applyHint(j)
			return true
		}
		j.Status = store.StatusDone
		return true
	}); err != nil {
		log.Printf("jobs: save final state for %s: %v", job.ID, err)
	}
}

func (m *Manager) bumpStatusError(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusErrors[id]++
	return m.statusErrors[id]
}

func (m *Manager) resetStatusError(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.statusErrors, id)
}

// markInterrupted moves a job out of "running" so the user can continue it.
func (m *Manager) markInterrupted(id, reason string) {
	if err := m.store.UpdateJob(id, func(j *store.Job) bool {
		if j.Status != store.StatusRunning {
			return false
		}
		j.Status = store.StatusInterrupted
		j.Error = reason
		j.ErrorTitle = "Скачивание прервано"
		j.ErrorAdvice = "Нажмите «Продолжить» — скачанные файлы не будут скачаны снова."
		j.FinishedAt = time.Now().UTC()
		return true
	}); err != nil {
		log.Printf("jobs: mark %s interrupted: %v", id, err)
	}
}

// Check runs operations/check between source and destination of a job.
func (m *Manager) Check(ctx context.Context, id string) (store.CheckResult, error) {
	job, ok := m.store.Job(id)
	if !ok {
		return store.CheckResult{}, fmt.Errorf("job %s not found", id)
	}

	req := map[string]any{
		"dstFs":        job.Dest,
		"oneWay":       true,
		"missingOnDst": true,
		"differ":       true,
		"error":        true,
	}
	srcFs, filter := SourceFS(remoteOf(job.Source), sourceForJob(job))
	req["srcFs"] = srcFs
	if filter != nil {
		req["_filter"] = filter
	}
	var out struct {
		HashType     string   `json:"hashType"`
		MissingOnDst []string `json:"missingOnDst"`
		Differ       []string `json:"differ"`
		Error        []string `json:"error"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	if err := m.rc.Call(callCtx, "operations/check", req, &out); err != nil {
		return store.CheckResult{}, err
	}

	hashType := out.HashType
	if hashType == "" || hashType == "none" {
		hashType = "size"
	}
	result := store.CheckResult{
		HashType:     hashType,
		Differ:       len(out.Differ),
		MissingOnDst: len(out.MissingOnDst),
		Broken:       len(out.Error),
		CheckedAt:    time.Now().UTC(),
	}
	if err := m.store.UpdateJob(id, func(j *store.Job) bool {
		j.Check = &result
		return true
	}); err != nil {
		return store.CheckResult{}, err
	}
	return result, nil
}

// Run polls until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(m.opts.PollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Poll(ctx)
		}
	}
}

// applyHint enriches a failed job with a human explanation when the error is
// one of the known patterns.
func applyHint(job *store.Job) {
	explanation := humanize.Explain(job.Error)
	job.ErrorTitle = explanation.Title
	job.ErrorAdvice = explanation.Advice
}

// transferLimit returns rclone's --max-transfer value for a destination,
// keeping a safety margin of ten percent.
func transferLimit(dest string) (string, bool) {
	free, err := disk.Free(dest)
	if err != nil || free < 1<<30 {
		return "", false
	}
	limit := disk.TransferLimit(free)
	if limit >= 1<<30 {
		return fmt.Sprintf("%dG", limit/(1<<30)), true
	}
	return fmt.Sprintf("%dM", limit/(1<<20)), true
}

func newGroup(id string) string {
	return fmt.Sprintf("migrate/%s/%d", id, time.Now().UTC().UnixNano())
}

func newID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(buf), nil
}
