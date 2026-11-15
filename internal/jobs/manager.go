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

// maxPollBackoff caps the pause between polls while rclone is unreachable.
const maxPollBackoff = 30 * time.Second

// ErrConflict means the job is in a state that does not allow the action.
var ErrConflict = errors.New("job state conflict")

// ErrInvalidMode means the caller asked for an unknown transfer mode.
var ErrInvalidMode = errors.New("unknown transfer mode")

// ErrSyncWithFiles means sync mode was combined with individual files. Sync
// deletes extra destination files, so a partial source would be dangerous; the
// UI only offers copy for a file selection and the API states it explicitly.
var ErrSyncWithFiles = errors.New("sync mode cannot download individual files")

// ErrSourceNotFound means a selected cloud folder does not exist or is not a
// directory. It is a user mistake, not an outage, so the HTTP layer answers 400.
var ErrSourceNotFound = errors.New("cloud source not found")

// SourceNotFoundError names the folder that failed verification.
type SourceNotFoundError struct {
	Path string
}

func (e *SourceNotFoundError) Error() string { return "cloud source not found: " + e.Path }

func (e *SourceNotFoundError) Unwrap() error { return ErrSourceNotFound }

// ErrSourceFileNotFound means a selected cloud file does not exist or is a
// folder. Like a missing folder it is a user mistake, so the HTTP layer answers
// 400.
var ErrSourceFileNotFound = errors.New("cloud source file not found")

// SourceFileError names the selected file that failed verification. IsDir is
// true when the path exists but is a folder, so the UI can explain the mix-up.
type SourceFileError struct {
	Path  string
	IsDir bool
}

func (e *SourceFileError) Error() string {
	if e.IsDir {
		return "cloud source is a folder, not a file: " + e.Path
	}
	return "cloud source file not found: " + e.Path
}

func (e *SourceFileError) Unwrap() error { return ErrSourceFileNotFound }

// Source is the cloud side of a job. Path is one legacy folder whose contents
// go straight into Dest. Paths is the multi-folder selection whose structure is
// preserved inside Dest; Files lists individual files picked alongside the
// folders. Paths and Files win over Path when set.
type Source struct {
	Path  string
	Paths []string
	Files []string
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

// FileIncludeRules returns the rclone include rules that select exactly the
// given files. A rule without a trailing "/**" matches one file rather than its
// subtree, so a selected file does not pull in a same-named folder. The leading
// slash keeps the match relative to the cloud root, as with folder rules.
func FileIncludeRules(files []string) []string {
	rules := make([]string, 0, len(files))
	for _, f := range files {
		rules = append(rules, "/"+escapeIncludeRule(f))
	}
	return rules
}

// SourceFS returns the rclone srcFs for a source and, for a folder or file
// selection, the _filter block that restricts the transfer to it.
func SourceFS(remote string, src Source) (string, map[string]any) {
	if len(src.Paths) > 0 || len(src.Files) > 0 {
		rules := make([]string, 0, len(src.Paths)+len(src.Files))
		rules = append(rules, IncludeRules(src.Paths)...)
		rules = append(rules, FileIncludeRules(src.Files)...)
		return remote + ":", map[string]any{"IncludeRule": rules}
	}
	if src.Path != "" {
		return remote + ":/" + strings.TrimPrefix(src.Path, "/"), nil
	}
	return remote + ":", nil
}

// VerifySources checks that every selected cloud path exists and has the
// expected type before a transfer starts: folders must be directories and files
// must be files. A missing or mistyped selection is ErrSourceNotFound or
// ErrSourceFileNotFound (and names the path); anything else is an rclone
// failure the caller must report as an outage rather than a bad request.
func VerifySources(ctx context.Context, caller Caller, remote string, src Source) error {
	for _, p := range selectedPaths(src) {
		if err := verifySource(ctx, caller, remote, p, true); err != nil {
			return err
		}
	}
	for _, p := range src.Files {
		if err := verifySource(ctx, caller, remote, p, false); err != nil {
			return err
		}
	}
	return nil
}

// verifySource asks rclone's operations/stat about one path. wantDir selects
// whether the path must be a folder or a file.
func verifySource(ctx context.Context, caller Caller, remote, p string, wantDir bool) error {
	var out struct {
		Item *struct {
			IsDir bool `json:"IsDir"`
		} `json:"item"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := caller.Call(callCtx, "operations/stat", map[string]any{"fs": remote + ":", "remote": p}, &out)
	cancel()
	notFound := func() error {
		if wantDir {
			return &SourceNotFoundError{Path: p}
		}
		return &SourceFileError{Path: p}
	}
	if err != nil {
		if IsMissingSource(err) {
			return notFound()
		}
		return fmt.Errorf("verify %s: %w", p, err)
	}
	if out.Item == nil {
		return notFound()
	}
	if wantDir && !out.Item.IsDir {
		return notFound()
	}
	if !wantDir && out.Item.IsDir {
		return &SourceFileError{Path: p, IsDir: true}
	}
	return nil
}

// IsMissingSource reports whether an rclone error means "there is no such
// folder", as opposed to an auth or network failure. The HTTP layer uses it to
// tell a missing cloud folder from an outage when it sizes folders for the UI.
func IsMissingSource(err error) bool {
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

// isMissingJob reports whether rclone answered that the engine job does not
// exist. Only such an answer proves the transfer is gone: a transport failure
// or another rc error means the job may still be running.
func isMissingJob(err error) bool {
	var apiErr *rc.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Status == http.StatusNotFound {
		return true
	}
	body := strings.ToLower(apiErr.Body)
	for _, marker := range []string{"couldn't find job", "job not found", "no such job"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// sourceForJob reconstructs the cloud source of a stored job, including jobs
// written by older builds that only have Source.
func sourceForJob(job store.Job) Source {
	if len(job.SourcePaths) > 0 || len(job.SourceFiles) > 0 {
		return Source{
			Paths: append([]string(nil), job.SourcePaths...),
			Files: append([]string(nil), job.SourceFiles...),
		}
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
	// StatusErrorGrace is how long a running job may keep answering rclone
	// errors before it is treated as lost. Without it an engine that answers
	// errors for ever would leave the job "running" in the UI indefinitely.
	StatusErrorGrace time.Duration
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
	if o.StatusErrorGrace <= 0 {
		o.StatusErrorGrace = 2 * time.Minute
	}
	return o
}

// Manager starts jobs, polls rclone and keeps store.Job records up to date.
type Manager struct {
	store *store.Store
	rc    Caller
	opts  Options

	statusErrorSince map[string]time.Time

	createMu     sync.Mutex
	mu           sync.Mutex
	statusErrors map[string]int
}

// NewManager wires a manager. Call Recover once at startup.
func NewManager(st *store.Store, client Caller, opts Options) *Manager {
	return &Manager{
		store:            st,
		rc:               client,
		opts:             opts.withDefaults(),
		statusErrors:     map[string]int{},
		statusErrorSince: map[string]time.Time{},
	}
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

// payloadErrorCaller is implemented by rc clients that can call methods whose
// "error" field is payload, not a transport failure.
type payloadErrorCaller interface {
	CallWithPayloadError(ctx context.Context, method string, in, out any) error
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
	// job/status reports a finished-but-failed transfer with success=false and
	// a top-level "error" string. That error is the result of the transfer, not
	// a failed rc call, so it must land in the status payload.
	caller, ok := m.rc.(payloadErrorCaller)
	if !ok {
		err := m.rc.Call(callCtx, "job/status", map[string]any{"jobid": job.RcloneJobID}, &status)
		return status, err
	}
	err := caller.CallWithPayloadError(callCtx, "job/status", map[string]any{"jobid": job.RcloneJobID}, &status)
	return status, err
}

// sameTransfer reports whether an engine status answer belongs to job's own
// transfer rather than to a reused job id from a restarted rclone.
func sameTransfer(job store.Job, status rcloneStatus) bool {
	return job.ExecuteID == "" || status.ExecuteID == "" || status.ExecuteID == job.ExecuteID
}

// oldJobAlive reports whether the rclone job behind job is still running and
// belongs to this record (same executeId). A non-nil error means the engine
// could not prove the transfer is gone, so Resume must not start a second one.
func (m *Manager) oldJobAlive(ctx context.Context, job store.Job) (bool, error) {
	if job.RcloneJobID == 0 {
		return false, nil
	}
	status, err := m.statusOf(ctx, job)
	if err != nil {
		if rc.IsUnavailable(err) || !isMissingJob(err) {
			return false, err
		}
		// rclone answered and no longer knows the job: resuming is safe.
		return false, nil
	}
	return !status.Finished && sameTransfer(job, status), nil
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
	if mode == "sync" && len(src.Files) > 0 {
		return store.Job{}, ErrSyncWithFiles
	}
	if err := VerifySources(ctx, m.rc, account.Remote, src); err != nil {
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
	if len(src.Files) > 0 {
		job.SourceFiles = append([]string(nil), src.Files...)
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
	// A folder or file may have disappeared while the job was paused. Verify
	// before the job is claimed again: a sync into a vanished folder would
	// empty its destination and a missing file would silently transfer less.
	src := sourceForJob(job)
	if len(selectedPaths(src)) > 0 || len(src.Files) > 0 {
		remote := remoteOf(job.Source)
		if remote == "" {
			return store.Job{}, fmt.Errorf("job %s has no cloud source", id)
		}
		if err := VerifySources(ctx, m.rc, remote, src); err != nil {
			return store.Job{}, err
		}
	}
	// The previous record may point at an rclone job that is still running:
	// starting a second transfer into the same folder would corrupt it. If the
	// engine cannot be asked at all, refuse instead of risking a second copy.
	alive, err := m.oldJobAlive(ctx, job)
	if err != nil {
		return store.Job{}, fmt.Errorf("check previous rclone job for %s: %w", id, err)
	}
	if alive {
		return store.Job{}, ErrConflict
	}

	group := newGroup(id)
	startedAt := time.Now().UTC()
	claimed := false
	err = m.claimDest(job.Dest, func() error {
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

// Poll refreshes every running job once. It reports whether rclone answered
// every status request: false means the engine is unreachable, so Run backs off
// instead of hammering a socket that is still starting.
func (m *Manager) Poll(ctx context.Context) bool {
	reachable := true
	for _, job := range m.store.Jobs() {
		if job.Status != store.StatusRunning {
			continue
		}
		if !m.pollOne(ctx, job) {
			reachable = false
		}
	}
	return reachable
}

func (m *Manager) pollOne(ctx context.Context, job store.Job) bool {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	reachable := true
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
	statsErr := m.rc.Call(callCtx, "core/stats", map[string]any{"group": job.Group}, &stats)
	if statsErr == nil {
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
	} else if rc.IsUnavailable(statsErr) {
		reachable = false
	}

	// A stored job without an engine job cannot be followed: it is already
	// gone, so there is nothing to stop and nothing to wait for.
	if job.RcloneJobID == 0 {
		m.markInterrupted(job.ID, "Скачивание прервалось: приложение перезапустилось.")
		return reachable
	}

	status, err := m.statusOf(ctx, job)
	if err != nil {
		if rc.IsUnavailable(err) {
			// rclone itself is unreachable, but the transfer may still be
			// writing files. A transport failure is not a lost transfer: keep
			// the job running and retry on the next poll.
			log.Printf("jobs: poll %s: rclone is unavailable: %v", job.ID, err)
			return false
		}
		if !isMissingJob(err) {
			// rclone answered with some other error. The job may well be alive,
			// so a single failure is not a lost transfer; a later poll will tell.
			log.Printf("jobs: poll %s: rclone answered with %v", job.ID, err)
			if m.statusErrorLongEnough(job.ID) {
				m.stopRclone(ctx, job.RcloneJobID)
				m.markInterrupted(job.ID, "Скачивание прервалось: движок не сообщает статус.")
			}
			return reachable
		}
		if failures := m.bumpStatusError(job.ID); failures >= maxStatusFailures {
			m.resetStatusError(job.ID)
			// Stop the engine job before marking the record interrupted: if it
			// is still transferring, a later resume would start a second copy
			// into the same tree.
			m.stopRclone(ctx, job.RcloneJobID)
			m.markInterrupted(job.ID, "Скачивание прервалось: приложение перезапустилось.")
		}
		return reachable
	}
	m.resetStatusError(job.ID)

	// A re-used job id from a restarted rclone belongs to a different transfer.
	if !sameTransfer(job, status) {
		m.markInterrupted(job.ID, "Скачивание прервалось: приложение перезапустилось.")
		return true
	}
	if !status.Finished {
		return true
	}
	m.finalize(job, status)
	return true
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
	delete(m.statusErrorSince, id)
}

// statusErrorLongEnough reports whether a job has been answering rclone errors
// for longer than StatusErrorGrace, i.e. the engine is not coming back.
func (m *Manager) statusErrorLongEnough(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.statusErrorSince == nil {
		m.statusErrorSince = make(map[string]time.Time)
	}
	since, ok := m.statusErrorSince[id]
	if !ok {
		m.statusErrorSince[id] = time.Now()
		return false
	}
	return time.Since(since) >= m.opts.StatusErrorGrace
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

// Run polls until ctx is done. While rclone is unreachable the delay doubles,
// up to maxPollBackoff, so a restarting engine is not hit every couple of
// seconds; the first successful poll restores the normal cadence.
func (m *Manager) Run(ctx context.Context) {
	delay := m.opts.PollEvery
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if m.Poll(ctx) {
				delay = m.opts.PollEvery
			} else {
				delay = nextPollDelay(delay)
			}
			timer.Reset(delay)
		}
	}
}

// nextPollDelay doubles the wait between polls, capped at maxPollBackoff.
func nextPollDelay(delay time.Duration) time.Duration {
	next := delay * 2
	if next > maxPollBackoff {
		return maxPollBackoff
	}
	return next
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
