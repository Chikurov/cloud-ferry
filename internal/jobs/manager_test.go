package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

type fakeRC struct {
	mu        sync.Mutex
	calls     []string
	inputs    []map[string]any
	responses map[string]any
	errors    map[string]error
	statItems map[string]any
	onCall    func(method string)
}

func (f *fakeRC) Call(_ context.Context, method string, in, out any) error {
	f.mu.Lock()
	f.calls = append(f.calls, method)
	var input map[string]any
	if typed, ok := in.(map[string]any); ok {
		copied := make(map[string]any, len(typed))
		for key, value := range typed {
			copied[key] = value
		}
		input = copied
	}
	f.inputs = append(f.inputs, input)
	failErr := f.errors[method]
	response, ok := f.responses[method]
	if method == "operations/stat" && f.statItems != nil {
		remote, _ := input["remote"].(string)
		if item, exists := f.statItems[remote]; exists {
			response, ok = map[string]any{"item": item}, true
		}
	}
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall(method)
	}
	if failErr != nil {
		return failErr
	}
	if !ok || out == nil {
		return nil
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, out)
}

// CallWithPayloadError mirrors Call: tests drive the payload shape through
// responses, and the client itself is covered in the rc package.
func (f *fakeRC) CallWithPayloadError(ctx context.Context, method string, in, out any) error {
	return f.Call(ctx, method, in, out)
}

// setStat makes operations/stat answer for one cloud path. A nil item means
// the path does not exist; {"IsDir": ...} tells a folder from a file.
func (f *fakeRC) setStat(remote string, item map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statItems == nil {
		f.statItems = map[string]any{}
	}
	f.statItems[remote] = item
}

// inputFor returns the recorded input of the last call to method.
func (f *fakeRC) inputFor(t *testing.T, method string) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i] == method {
			return f.inputs[i]
		}
	}
	t.Fatalf("%s was not called", method)
	return nil
}

func (f *fakeRC) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, call := range f.calls {
		if call == method {
			total++
		}
	}
	return total
}

func newTestManager(t *testing.T) (*Manager, *fakeRC, *store.Store) {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	fake := &fakeRC{responses: map[string]any{
		"sync/copy":        map[string]any{"jobid": 7},
		"sync/sync":        map[string]any{"jobid": 8},
		"core/stats":       map[string]any{"bytes": 100, "totalBytes": 1000, "speed": 50, "transfers": 2, "totalTransfers": 10, "eta": 18},
		"job/status":       map[string]any{"finished": true, "success": true},
		"operations/stat":  map[string]any{"item": map[string]any{"IsDir": true}},
		"operations/check": map[string]any{"hashType": "md5", "match": []string{"ok/1", "ok/2"}, "differ": []string{"a/b"}, "missingOnDst": []string{}, "error": []string{}},
	}}
	return NewManager(st, fake, Options{Transfers: 4}), fake, st
}

func TestStartAndPollToDone(t *testing.T) {
	manager, fake, st := newTestManager(t)
	account := store.Account{ID: "a1", Name: "Яндекс", Remote: "cf_a1"}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	job, err := manager.Start(context.Background(), account, Source{Path: "/фото"}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.Source != "cf_a1:/фото" {
		t.Fatalf("source = %q", job.Source)
	}
	if job.Status != store.StatusRunning || job.RcloneJobID != 7 {
		t.Fatalf("job = %+v", job)
	}

	manager.Poll(context.Background())

	stored, ok := st.Job(job.ID)
	if !ok {
		t.Fatal("job not stored")
	}
	if stored.Status != store.StatusDone {
		t.Fatalf("status = %q, error = %q", stored.Status, stored.Error)
	}
	if stored.Stats.Bytes != 100 || stored.Stats.TotalBytes != 1000 {
		t.Fatalf("stats = %+v", stored.Stats)
	}
	if got := fake.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", got)
	}
}

func TestStartRejectsUnknownMode(t *testing.T) {
	manager, _, _ := newTestManager(t)
	if _, err := manager.Start(context.Background(), store.Account{Remote: "cf_a"}, Source{}, "/downloads", "move"); err == nil {
		t.Fatal("expected error")
	}
}

func TestResumeReissuesCopy(t *testing.T) {
	manager, fake, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	job, err := manager.Start(context.Background(), account, Source{}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := st.UpdateJob(job.ID, func(j *store.Job) bool {
		j.Status = store.StatusInterrupted
		return true
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	resumed, err := manager.Resume(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != store.StatusRunning {
		t.Fatalf("status = %q", resumed.Status)
	}

	copies := 0
	for _, call := range fake.calls {
		if call == "sync/copy" {
			copies++
		}
	}
	if copies != 2 {
		t.Fatalf("sync/copy calls = %d", copies)
	}
}

func TestRecoverMarksRunningAsInterrupted(t *testing.T) {
	manager, _, st := newTestManager(t)
	job := store.Job{ID: "j1", Status: store.StatusRunning, Source: "cf_a1:", Dest: "/downloads"}
	if err := st.AddJob(job); err != nil {
		t.Fatalf("add job: %v", err)
	}
	if err := manager.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	stored, _ := st.Job("j1")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q", stored.Status)
	}
}

func TestResumeRejectsDestConflictWithActiveJob(t *testing.T) {
	manager, fake, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	if _, err := manager.Start(context.Background(), account, Source{}, "/downloads/x", "copy"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	// A paused job into the same tree: Start would refuse this one, so it is
	// seeded directly to model a job that was interrupted before the active one
	// appeared.
	if err := st.AddJob(store.Job{
		ID: "paused", AccountID: "a1", Status: store.StatusInterrupted,
		Source: "cf_a1:", Dest: "/downloads/x", Group: "migrate/paused", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add paused job: %v", err)
	}

	before := fake.count("sync/copy")
	_, err := manager.Resume(context.Background(), "paused")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if got := fake.count("sync/copy"); got != before {
		t.Fatalf("sync/copy calls = %d, want %d: a conflicting resume must not launch", got, before)
	}
	stored, _ := st.Job("paused")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted (job must not be claimed)", stored.Status)
	}
}

func TestResumeRejectsNestedDestConflictWithActiveJob(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if _, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{}, "/downloads/tree/sub", "copy"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	// The paused job would write into the parent of the active tree.
	if err := st.AddJob(store.Job{
		ID: "paused", AccountID: "a1", Status: store.StatusInterrupted,
		Source: "cf_a1:", Dest: "/downloads/tree", Group: "migrate/paused", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add paused job: %v", err)
	}

	before := fake.count("sync/copy")
	if _, err := manager.Resume(context.Background(), "paused"); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if got := fake.count("sync/copy"); got != before {
		t.Fatalf("sync/copy calls = %d, want %d", got, before)
	}
}

func TestResumeRejectsChildDestConflictWithActiveJob(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if _, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{}, "/downloads/tree", "copy"); err != nil {
		t.Fatalf("first start: %v", err)
	}
	// The paused job would write inside the active tree.
	if err := st.AddJob(store.Job{
		ID: "paused", AccountID: "a1", Status: store.StatusInterrupted,
		Source: "cf_a1:", Dest: "/downloads/tree/sub", Group: "migrate/paused", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add paused job: %v", err)
	}

	before := fake.count("sync/copy")
	if _, err := manager.Resume(context.Background(), "paused"); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if got := fake.count("sync/copy"); got != before {
		t.Fatalf("sync/copy calls = %d, want %d", got, before)
	}
}

func TestRecoverFinalisesJobFinishedWhileDown(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.responses["job/status"] = map[string]any{
		"finished": true, "success": true, "executeId": "exec-1",
		"output": map[string]any{"error": []string{}},
	}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, ExecuteID: "exec-1",
		Source: "cf_a1:", Dest: "/downloads/yandex", Group: "migrate/j1",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	if err := manager.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusDone {
		t.Fatalf("status = %q, error = %q", stored.Status, stored.Error)
	}
	if stored.FinishedAt.IsZero() {
		t.Fatal("finishedAt is not set")
	}
}

func TestRecoverFinishesFailedJobWithHint(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.responses["job/status"] = map[string]any{
		"finished": true, "success": false, "error": "error listing: [410 - GoneError] Gone",
		"executeId": "exec-1",
		"output":    map[string]any{"error": []string{"фото/a.jpg"}},
	}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, ExecuteID: "exec-1",
		Source: "cf_a1:", Dest: "/downloads/yandex", Group: "migrate/j1",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	if err := manager.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusFailed || stored.ErrorTitle == "" {
		t.Fatalf("stored = %+v", stored)
	}
	if stored.FailureCount != 1 || len(stored.Failures) != 1 || stored.Failures[0] != "фото/a.jpg" {
		t.Fatalf("failures = %+v", stored.Failures)
	}
}

func TestRecoverLeavesRunningJobWhenRcloneUnavailable(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.errors = map[string]error{"job/status": errors.New("connect: connection refused")}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7,
		Source: "cf_a1:", Dest: "/downloads/yandex", Group: "migrate/j1",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	if err := manager.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	stored, _ := st.Job("j1")
	if stored.Status != store.StatusRunning {
		t.Fatalf("status = %q, want running while rclone is unreachable", stored.Status)
	}
}

func TestResumeRefusesWhenOldRcloneJobStillRuns(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.responses["job/status"] = map[string]any{"finished": false, "executeId": "exec-1"}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusInterrupted, RcloneJobID: 7, ExecuteID: "exec-1",
		Source: "cf_a1:", Dest: "/downloads/yandex", Group: "g",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	_, err := manager.Resume(context.Background(), "j1")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if got := fake.count("sync/copy"); got != 0 {
		t.Fatalf("sync/copy calls = %d, want 0", got)
	}
}

func TestResumeAllowsFinishedOldRcloneJob(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.responses["job/status"] = map[string]any{"finished": true, "success": false, "executeId": "exec-1"}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusInterrupted, RcloneJobID: 7, ExecuteID: "exec-1",
		Source: "cf_a1:", Dest: "/downloads/yandex", Group: "g",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	resumed, err := manager.Resume(context.Background(), "j1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != store.StatusRunning {
		t.Fatalf("status = %q", resumed.Status)
	}
	if got := fake.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", got)
	}
}

func TestResumeRefusesWhenOldJobStatusIsUnknown(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.errors = map[string]error{"job/status": errors.New("dial unix /run/rclone/rclone.sock: connect: connection refused")}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusInterrupted, RcloneJobID: 7, ExecuteID: "exec-1",
		Source: "cf_a1:", Dest: "/downloads/yandex", Group: "g",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if _, err := manager.Resume(context.Background(), "j1"); err == nil || errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want a retryable failure instead of a second launch", err)
	}
	if got := fake.count("sync/copy"); got != 0 {
		t.Fatalf("sync/copy calls = %d, want 0", got)
	}
	stored, _ := st.Job("j1")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted (job must not be claimed)", stored.Status)
	}
}

func TestCheckSummarisesResult(t *testing.T) {
	manager, _, st := newTestManager(t)
	job := store.Job{ID: "j1", Status: store.StatusDone, Source: "cf_a1:", Dest: "/downloads/yandex"}
	if err := st.AddJob(job); err != nil {
		t.Fatalf("add job: %v", err)
	}

	result, err := manager.Check(context.Background(), "j1")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if result.HashType != "md5" || result.Differ != 1 || result.MissingOnDst != 0 || result.Matched != 2 {
		t.Fatalf("result = %+v", result)
	}
	stored, _ := st.Job("j1")
	if stored.Check == nil || stored.Check.Differ != 1 || stored.Check.Matched != 2 {
		t.Fatalf("stored check = %+v", stored.Check)
	}
}

func TestFailedJobGetsHumanHint(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.responses["job/status"] = map[string]any{"finished": true, "success": false, "error": "error listing: [410 - GoneError] Gone"}
	account := store.Account{ID: "a1", Remote: "cf_a1"}

	job, err := manager.Start(context.Background(), account, Source{}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	manager.Poll(context.Background())

	stored, _ := st.Job(job.ID)
	if stored.Status != store.StatusFailed {
		t.Fatalf("status = %q", stored.Status)
	}
	if stored.ErrorTitle != "Яндекс отключил этот способ входа" || stored.ErrorAdvice == "" {
		t.Fatalf("hint = %q / %q", stored.ErrorTitle, stored.ErrorAdvice)
	}
}

func TestTransferLimit(t *testing.T) {
	limit, ok := transferLimit(t.TempDir())
	if !ok {
		t.Fatal("expected a limit for a temp dir")
	}
	if !strings.HasSuffix(limit, "G") && !strings.HasSuffix(limit, "M") {
		t.Fatalf("limit = %q", limit)
	}
}

func TestFailedJobStoresFileList(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.responses["job/status"] = map[string]any{
		"finished": true,
		"success":  false,
		"error":    "2 errors while transferring",
		"output":   map[string]any{"error": []string{"фото/a.jpg", "фото/b.jpg"}},
	}
	job, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	manager.Poll(context.Background())

	stored, _ := st.Job(job.ID)
	if stored.FailureCount != 2 || len(stored.Failures) != 2 {
		t.Fatalf("failures = %+v (count %d)", stored.Failures, stored.FailureCount)
	}
	if stored.Failures[0] != "фото/a.jpg" {
		t.Fatalf("failures = %v", stored.Failures)
	}
}

func TestStopFallsBackToLocalCancel(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.errors = map[string]error{"job/stop": errors.New("connection refused")}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, Group: "migrate/j1",
		Source: "cf_a1:", Dest: "/downloads/yandex",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	warning, err := manager.Stop(context.Background(), "j1")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if warning == "" {
		t.Fatal("expected a warning about rclone")
	}
	stored, _ := st.Job("j1")
	if stored.Status != store.StatusCanceled {
		t.Fatalf("status = %q", stored.Status)
	}
}

func TestStopWithoutRcloneJobCancelsLocally(t *testing.T) {
	manager, _, st := newTestManager(t)
	if err := st.AddJob(store.Job{ID: "j1", Status: store.StatusQueued}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	warning, err := manager.Stop(context.Background(), "j1")
	if err != nil || warning != "" {
		t.Fatalf("stop: warning=%q err=%v", warning, err)
	}
	stored, _ := st.Job("j1")
	if stored.Status != store.StatusCanceled {
		t.Fatalf("status = %q", stored.Status)
	}
}

func TestStopUnknownJob(t *testing.T) {
	manager, _, _ := newTestManager(t)
	if _, err := manager.Stop(context.Background(), "nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPollMarksJobInterruptedWhenRcloneLostIt(t *testing.T) {
	manager, fake, st := newTestManager(t)
	// An rc API error means rclone answered and does not know this job: the
	// transfer is really gone and the record must become interruptible.
	fake.errors = map[string]error{"job/status": &rc.APIError{Method: "job/status", Status: http.StatusInternalServerError, Body: "couldn't find job"}}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, Group: "migrate/j1",
		Source: "cf_a1:", Dest: "/downloads/yandex",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	for i := 0; i < maxStatusFailures; i++ {
		manager.Poll(context.Background())
	}

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q", stored.Status)
	}
	if stored.ErrorAdvice == "" {
		t.Fatal("expected advice on how to continue")
	}
	// The engine job is stopped before the record is declared interrupted, so
	// a resume cannot start a second transfer into the same tree.
	if got := fake.count("job/stop"); got != 1 {
		t.Fatalf("job/stop calls = %d, want 1", got)
	}
}

func TestPollKeepsJobRunningWhenRcloneIsUnavailable(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.errors = map[string]error{"job/status": errors.New("dial unix /run/rclone/rclone.sock: connect: connection refused")}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, Group: "migrate/j1",
		Source: "cf_a1:", Dest: "/downloads/yandex",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	for i := 0; i < maxStatusFailures*3; i++ {
		if manager.Poll(context.Background()) {
			t.Fatal("Poll reported a reachable engine while the socket was down")
		}
	}

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusRunning {
		t.Fatalf("status = %q, want running after transport failures", stored.Status)
	}
	if got := fake.count("job/stop"); got != 0 {
		t.Fatalf("job/stop calls = %d, want 0", got)
	}

	// When rclone answers again and has lost the job, the usual threshold
	// still applies.
	fake.errors["job/status"] = &rc.APIError{Method: "job/status", Status: http.StatusInternalServerError, Body: "couldn't find job"}
	for i := 0; i < maxStatusFailures; i++ {
		if !manager.Poll(context.Background()) {
			t.Fatal("Poll reported an unreachable engine after rclone answered")
		}
	}
	stored, _ = st.Job("j1")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted after a real lost-job answer", stored.Status)
	}
}

func TestPollKeepsJobRunningOnOtherStatusError(t *testing.T) {
	manager, fake, st := newTestManager(t)
	// rclone answered, but not with "job not found". That does not prove the
	// transfer is gone, so the record must stay resumable as running.
	fake.errors = map[string]error{"job/status": &rc.APIError{Method: "job/status", Status: http.StatusInternalServerError, Body: "internal error"}}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, Group: "migrate/j1",
		Source: "cf_a1:", Dest: "/downloads/yandex",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	for i := 0; i < maxStatusFailures*3; i++ {
		manager.Poll(context.Background())
	}

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusRunning {
		t.Fatalf("status = %q, want running after non-missing status errors", stored.Status)
	}
	if got := fake.count("job/stop"); got != 0 {
		t.Fatalf("job/stop calls = %d, want 0", got)
	}
}

func TestNextPollDelayDoublesAndCaps(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want time.Duration
	}{
		{2 * time.Second, 4 * time.Second},
		{4 * time.Second, 8 * time.Second},
		{8 * time.Second, 16 * time.Second},
		{16 * time.Second, maxPollBackoff},
		{maxPollBackoff, maxPollBackoff},
	}
	for _, tc := range cases {
		if got := nextPollDelay(tc.in); got != tc.want {
			t.Fatalf("nextPollDelay(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestPollDoesNotOverwriteCanceledJob(t *testing.T) {
	manager, _, st := newTestManager(t)
	stale := store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, Group: "migrate/j1",
		Source: "cf_a1:", Dest: "/downloads/yandex",
	}
	if err := st.AddJob(stale); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := manager.Stop(context.Background(), "j1"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	manager.pollOne(context.Background(), stale)

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusCanceled {
		t.Fatalf("status = %q, want canceled", stored.Status)
	}
	if stored.Stats.Bytes != 0 {
		t.Fatalf("stale stats were written: %+v", stored.Stats)
	}
}

func TestPollIgnoresStaleRcloneJobID(t *testing.T) {
	manager, _, st := newTestManager(t)
	stale := store.Job{ID: "j1", Status: store.StatusRunning, RcloneJobID: 5, Group: "migrate/j1"}
	fresh := stale
	fresh.RcloneJobID = 9
	if err := st.AddJob(fresh); err != nil {
		t.Fatalf("add: %v", err)
	}

	manager.pollOne(context.Background(), stale)

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusRunning {
		t.Fatalf("status = %q, want running", stored.Status)
	}
	if stored.Stats.Bytes != 0 {
		t.Fatalf("stale stats were written: %+v", stored.Stats)
	}
}

func TestResumeClearsPreviousFailures(t *testing.T) {
	manager, _, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	job := store.Job{
		ID: "j1", AccountID: "a1", Status: store.StatusFailed, Group: "migrate/j1",
		Source: "cf_a1:/фото", Dest: "/downloads/yandex",
		Error: "old", ErrorTitle: "Было", ErrorAdvice: "Совет",
		Failures: []string{"a.jpg"}, FailureCount: 1,
		Check: &store.CheckResult{Differ: 2},
	}
	if err := st.AddJob(job); err != nil {
		t.Fatalf("add job: %v", err)
	}

	resumed, err := manager.Resume(context.Background(), "j1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Error != "" || resumed.ErrorTitle != "" || resumed.ErrorAdvice != "" {
		t.Fatalf("error fields not cleared: %+v", resumed)
	}
	if resumed.FailureCount != 0 || len(resumed.Failures) != 0 || resumed.Check != nil {
		t.Fatalf("failure state not cleared: %+v", resumed)
	}
}

func TestResumeRunningJobConflicts(t *testing.T) {
	manager, _, st := newTestManager(t)
	if err := st.AddJob(store.Job{ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, Group: "g"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := manager.Resume(context.Background(), "j1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	stored, _ := st.Job("j1")
	if stored.Status != store.StatusRunning {
		t.Fatalf("status = %q", stored.Status)
	}
}

func TestStopFinishedJobKeepsResult(t *testing.T) {
	manager, _, st := newTestManager(t)
	if err := st.AddJob(store.Job{ID: "j1", Status: store.StatusDone}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := manager.Stop(context.Background(), "j1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	stored, _ := st.Job("j1")
	if stored.Status != store.StatusDone {
		t.Fatalf("status = %q", stored.Status)
	}
}

func TestPollIgnoresForeignExecuteID(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.responses["job/status"] = map[string]any{"finished": false, "executeId": "other-exec"}
	job := store.Job{ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, ExecuteID: "my-exec", Group: "g"}
	if err := st.AddJob(job); err != nil {
		t.Fatalf("add: %v", err)
	}

	manager.pollOne(context.Background(), job)

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted", stored.Status)
	}
}

func TestStartPersistsJobBeforeLaunch(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddAccount(store.Account{ID: "a1", Remote: "cf_a1"}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	queuedAtLaunch := false
	fake.onCall = func(method string) {
		if method != "sync/copy" {
			return
		}
		jobs := st.Jobs()
		if len(jobs) == 1 && jobs[0].Status == store.StatusQueued {
			queuedAtLaunch = true
		}
	}

	if _, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{}, "/downloads/x", "copy"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !queuedAtLaunch {
		t.Fatal("job must be persisted as queued before rclone is launched")
	}
}

func TestIncludeRulesEscape(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"Фото", "/Фото/**"},
		{"a*b", `/a\*b/**`},
		{"q?[x]{y}\\z", `/q\?\[x\]\{y\}\\z/**`},
		{"A/B", "/A/B/**"},
		{"with space & юникод", "/with space & юникод/**"},
	}
	for _, tc := range cases {
		rules := IncludeRules([]string{tc.path})
		if len(rules) != 1 || rules[0] != tc.want {
			t.Fatalf("%q: rules = %#v, want %q", tc.path, rules, tc.want)
		}
	}
}

func TestSourceFS(t *testing.T) {
	cases := []struct {
		name       string
		src        Source
		wantFS     string
		wantFilter bool
	}{
		{"multi", Source{Paths: []string{"Документы", "Фотографии/2024"}}, "cf_a1:", true},
		{"files only", Source{Files: []string{"a.txt", "b/c.md"}}, "cf_a1:", true},
		{"folders and files", Source{Paths: []string{"Фото"}, Files: []string{"a.txt"}}, "cf_a1:", true},
		{"legacy", Source{Path: "/Фото"}, "cf_a1:/Фото", false},
		{"whole disk", Source{}, "cf_a1:", false},
		{"explicit empty list", Source{Paths: []string{}}, "cf_a1:", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsPath, filter := SourceFS("cf_a1", tc.src)
			if fsPath != tc.wantFS {
				t.Fatalf("fs = %q, want %q", fsPath, tc.wantFS)
			}
			if (filter != nil) != tc.wantFilter {
				t.Fatalf("filter = %#v", filter)
			}
			if tc.wantFilter {
				rules, _ := filter["IncludeRule"].([]string)
				want := make([]string, 0, len(tc.src.Paths)+len(tc.src.Files))
				want = append(want, IncludeRules(tc.src.Paths)...)
				want = append(want, FileIncludeRules(tc.src.Files)...)
				if !reflect.DeepEqual(rules, want) {
					t.Fatalf("rules = %#v, want %#v", rules, want)
				}
			}
		})
	}
}

func TestStartMultiUsesRootAndFilter(t *testing.T) {
	manager, fake, st := newTestManager(t)
	account := store.Account{ID: "a1", Name: "Яндекс", Remote: "cf_a1"}
	src := Source{Paths: []string{"Документы", "Фотографии/2024"}}

	job, err := manager.Start(context.Background(), account, src, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.Source != "cf_a1:" {
		t.Fatalf("source = %q", job.Source)
	}
	if !reflect.DeepEqual(job.SourcePaths, src.Paths) {
		t.Fatalf("sourcePaths = %#v", job.SourcePaths)
	}
	if job.Status != store.StatusRunning {
		t.Fatalf("status = %q", job.Status)
	}
	stored, ok := st.Job(job.ID)
	if !ok || !reflect.DeepEqual(stored.SourcePaths, src.Paths) {
		t.Fatalf("stored job = %+v", stored)
	}

	if got := fake.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", got)
	}
	call := fake.inputFor(t, "sync/copy")
	if call["srcFs"] != "cf_a1:" {
		t.Fatalf("srcFs = %v", call["srcFs"])
	}
	filter, ok := call["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call)
	}
	rules, _ := filter["IncludeRule"].([]string)
	wantRules := []string{"/Документы/**", "/Фотографии/2024/**"}
	if !reflect.DeepEqual(rules, wantRules) {
		t.Fatalf("rules = %#v, want %#v", rules, wantRules)
	}
	if got := fake.count("operations/stat"); got != len(src.Paths) {
		t.Fatalf("operations/stat calls = %d, want %d", got, len(src.Paths))
	}
	stat := fake.inputFor(t, "operations/stat")
	if stat["fs"] != "cf_a1:" || stat["remote"] != "Фотографии/2024" {
		t.Fatalf("stat input = %#v", stat)
	}
}

func TestFileIncludeRulesEscape(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"a.txt", "/a.txt"},
		{"A/b c.md", "/A/b c.md"},
		{"a*b", `/a\*b`},
		{"q?[x]{y}\\z", `/q\?\[x\]\{y\}\\z`},
	}
	for _, tc := range cases {
		rules := FileIncludeRules([]string{tc.path})
		if len(rules) != 1 || rules[0] != tc.want {
			t.Fatalf("%q: rules = %#v, want %q", tc.path, rules, tc.want)
		}
	}
}

// TestStartFilesUsesRootAndExactRules checks the core of a file selection: one
// rclone source at the cloud root and an exact include rule per file.
func TestStartFilesUsesRootAndExactRules(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.setStat("a.txt", map[string]any{"IsDir": false})
	fake.setStat("b/c.md", map[string]any{"IsDir": false})
	account := store.Account{ID: "a1", Name: "Яндекс", Remote: "cf_a1"}
	src := Source{Files: []string{"a.txt", "b/c.md"}}

	job, err := manager.Start(context.Background(), account, src, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.Source != "cf_a1:" {
		t.Fatalf("source = %q", job.Source)
	}
	if job.SourcePaths != nil {
		t.Fatalf("sourcePaths = %#v, want nil", job.SourcePaths)
	}
	if !reflect.DeepEqual(job.SourceFiles, src.Files) {
		t.Fatalf("sourceFiles = %#v", job.SourceFiles)
	}
	stored, ok := st.Job(job.ID)
	if !ok || !reflect.DeepEqual(stored.SourceFiles, src.Files) {
		t.Fatalf("stored job = %+v", stored)
	}

	call := fake.inputFor(t, "sync/copy")
	if call["srcFs"] != "cf_a1:" {
		t.Fatalf("srcFs = %v", call["srcFs"])
	}
	filter, ok := call["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if !reflect.DeepEqual(rules, []string{"/a.txt", "/b/c.md"}) {
		t.Fatalf("rules = %#v", rules)
	}
	if got := fake.count("operations/stat"); got != len(src.Files) {
		t.Fatalf("operations/stat calls = %d, want %d", got, len(src.Files))
	}
	stat := fake.inputFor(t, "operations/stat")
	if stat["fs"] != "cf_a1:" || stat["remote"] != "b/c.md" {
		t.Fatalf("stat input = %#v", stat)
	}
}

func TestStartFilesWithFoldersKeepsBothRuleSets(t *testing.T) {
	manager, fake, _ := newTestManager(t)
	fake.setStat("Фото", map[string]any{"IsDir": true})
	fake.setStat("a.txt", map[string]any{"IsDir": false})
	src := Source{Paths: []string{"Фото"}, Files: []string{"a.txt"}}

	job, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, src, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !reflect.DeepEqual(job.SourcePaths, src.Paths) || !reflect.DeepEqual(job.SourceFiles, src.Files) {
		t.Fatalf("job = %+v", job)
	}
	call := fake.inputFor(t, "sync/copy")
	filter, ok := call["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if !reflect.DeepEqual(rules, []string{"/Фото/**", "/a.txt"}) {
		t.Fatalf("rules = %#v", rules)
	}
	if got := fake.count("operations/stat"); got != 2 {
		t.Fatalf("operations/stat calls = %d, want 2", got)
	}
}

func TestStartSyncWithFilesRejected(t *testing.T) {
	manager, fake, st := newTestManager(t)
	_, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Files: []string{"a.txt"}}, "/downloads/yandex", "sync")
	if !errors.Is(err, ErrSyncWithFiles) {
		t.Fatalf("err = %v, want ErrSyncWithFiles", err)
	}
	if got := fake.count("operations/stat"); got != 0 {
		t.Fatalf("operations/stat calls = %d, want 0: a rejected mode must not be verified", got)
	}
	if got := fake.count("sync/sync"); got != 0 {
		t.Fatalf("sync/sync calls = %d, want 0", got)
	}
	if jobs := st.Jobs(); len(jobs) != 0 {
		t.Fatalf("job persisted for a rejected mode: %+v", jobs)
	}
}

func TestStartMissingFileReturnsErrSourceFileNotFound(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.setStat("gone.txt", nil)

	_, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Files: []string{"gone.txt"}}, "/downloads/yandex", "copy")
	if !errors.Is(err, ErrSourceFileNotFound) {
		t.Fatalf("err = %v, want ErrSourceFileNotFound", err)
	}
	var fileErr *SourceFileError
	if !errors.As(err, &fileErr) || fileErr.Path != "gone.txt" || fileErr.IsDir {
		t.Fatalf("error does not name the missing file: %v", err)
	}
	if len(st.Jobs()) != 0 {
		t.Fatalf("job persisted despite a missing file: %+v", st.Jobs())
	}
	if got := fake.count("sync/copy"); got != 0 {
		t.Fatalf("sync/copy calls = %d, want 0", got)
	}
}

func TestStartFolderInsteadOfFileReturnsErrSourceFileNotFound(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.setStat("Фото", map[string]any{"IsDir": true})

	_, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Files: []string{"Фото"}}, "/downloads/yandex", "copy")
	if !errors.Is(err, ErrSourceFileNotFound) {
		t.Fatalf("err = %v, want ErrSourceFileNotFound", err)
	}
	var fileErr *SourceFileError
	if !errors.As(err, &fileErr) || fileErr.Path != "Фото" || !fileErr.IsDir {
		t.Fatalf("error does not report a folder: %v", err)
	}
	if len(st.Jobs()) != 0 {
		t.Fatalf("job persisted despite a folder source: %+v", st.Jobs())
	}
}

func TestStartFilesNetworkFailureIsOutage(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.errors = map[string]error{"operations/stat": errors.New("dial unix: connection refused")}

	_, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Files: []string{"a.txt"}}, "/downloads/yandex", "copy")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrSourceFileNotFound) || errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("network failure must not look like a missing file: %v", err)
	}
	if len(st.Jobs()) != 0 {
		t.Fatalf("job persisted during an outage: %+v", st.Jobs())
	}
}

func TestResumeWithFilesReissuesExactRules(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddAccount(store.Account{ID: "a1", Remote: "cf_a1"}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	fake.setStat("a.txt", map[string]any{"IsDir": false})
	fake.setStat("b/c.md", map[string]any{"IsDir": false})
	files := []string{"a.txt", "b/c.md"}
	job, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Files: files}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := st.UpdateJob(job.ID, func(j *store.Job) bool {
		j.Status = store.StatusInterrupted
		return true
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	resumed, err := manager.Resume(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != store.StatusRunning {
		t.Fatalf("status = %q", resumed.Status)
	}
	if !reflect.DeepEqual(resumed.SourceFiles, files) {
		t.Fatalf("sourceFiles = %#v", resumed.SourceFiles)
	}
	if got := fake.count("sync/copy"); got != 2 {
		t.Fatalf("sync/copy calls = %d, want 2", got)
	}
	if got := fake.count("operations/stat"); got != 2*len(files) {
		t.Fatalf("operations/stat calls = %d, want %d (start + resume)", got, 2*len(files))
	}
	call := fake.inputFor(t, "sync/copy")
	filter, ok := call["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("resume filter missing: %#v", call)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if !reflect.DeepEqual(rules, []string{"/a.txt", "/b/c.md"}) {
		t.Fatalf("rules = %#v", rules)
	}
}

func TestResumeWithFilesRefusesMissingFile(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddAccount(store.Account{ID: "a1", Remote: "cf_a1"}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	fake.setStat("a.txt", map[string]any{"IsDir": false})
	job, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Files: []string{"a.txt"}}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := st.UpdateJob(job.ID, func(j *store.Job) bool {
		j.Status = store.StatusInterrupted
		return true
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	fake.setStat("a.txt", nil)

	_, err = manager.Resume(context.Background(), job.ID)
	if !errors.Is(err, ErrSourceFileNotFound) {
		t.Fatalf("err = %v, want ErrSourceFileNotFound", err)
	}
	stored, _ := st.Job(job.ID)
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted (job must not be claimed)", stored.Status)
	}
	if got := fake.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", got)
	}
}

func TestResumeLegacyJobWithoutFilesUsesSourcePath(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.setStat("Фото", map[string]any{"IsDir": true})
	if err := st.AddJob(store.Job{
		ID: "legacy", Status: store.StatusInterrupted, Source: "cf_a1:/Фото",
		Dest: "/downloads/yandex", Group: "g", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	resumed, err := manager.Resume(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.SourceFiles != nil {
		t.Fatalf("sourceFiles = %#v, want nil", resumed.SourceFiles)
	}
	call := fake.inputFor(t, "sync/copy")
	if call["srcFs"] != "cf_a1:/Фото" {
		t.Fatalf("srcFs = %v", call["srcFs"])
	}
	if _, exists := call["_filter"]; exists {
		t.Fatalf("legacy job must not carry a filter: %#v", call)
	}
}

func TestCheckUsesFilterForFilesJob(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusDone, Source: "cf_a1:",
		SourceFiles: []string{"a.txt", "b/c.md"}, Dest: "/downloads/yandex",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	if _, err := manager.Check(context.Background(), "j1"); err != nil {
		t.Fatalf("check: %v", err)
	}
	call := fake.inputFor(t, "operations/check")
	if call["srcFs"] != "cf_a1:" {
		t.Fatalf("srcFs = %v", call["srcFs"])
	}
	filter, ok := call["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if !reflect.DeepEqual(rules, []string{"/a.txt", "/b/c.md"}) {
		t.Fatalf("rules = %#v", rules)
	}
}

func TestStartLegacyPathUnchanged(t *testing.T) {
	manager, fake, st := newTestManager(t)
	job, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Path: "/Фото"}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.Source != "cf_a1:/Фото" {
		t.Fatalf("source = %q", job.Source)
	}
	if job.SourcePaths != nil {
		t.Fatalf("sourcePaths = %#v, want nil", job.SourcePaths)
	}
	call := fake.inputFor(t, "sync/copy")
	if call["srcFs"] != "cf_a1:/Фото" {
		t.Fatalf("srcFs = %v", call["srcFs"])
	}
	if _, exists := call["_filter"]; exists {
		t.Fatalf("legacy job must not carry a filter: %#v", call)
	}
	if _, exists := st.Job(job.ID); !exists {
		t.Fatal("job not stored")
	}
}

func TestStartWholeDisk(t *testing.T) {
	manager, fake, st := newTestManager(t)
	job, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if job.Source != "cf_a1:" || job.SourcePaths != nil {
		t.Fatalf("job = %+v", job)
	}
	call := fake.inputFor(t, "sync/copy")
	if call["srcFs"] != "cf_a1:" {
		t.Fatalf("srcFs = %v", call["srcFs"])
	}
	if _, exists := call["_filter"]; exists {
		t.Fatalf("whole disk must not carry a filter: %#v", call)
	}
	if got := fake.count("operations/stat"); got != 0 {
		t.Fatalf("operations/stat calls = %d, want 0", got)
	}
	if _, exists := st.Job(job.ID); !exists {
		t.Fatal("job not stored")
	}
}

func TestResumeVerifiesAndReissuesFilter(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddAccount(store.Account{ID: "a1", Remote: "cf_a1"}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	job, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Paths: []string{"Фото"}}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := st.UpdateJob(job.ID, func(j *store.Job) bool {
		j.Status = store.StatusInterrupted
		return true
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	resumed, err := manager.Resume(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != store.StatusRunning || resumed.Group == job.Group {
		t.Fatalf("resumed = %+v", resumed)
	}
	if !reflect.DeepEqual(resumed.SourcePaths, []string{"Фото"}) {
		t.Fatalf("sourcePaths = %#v", resumed.SourcePaths)
	}
	if got := fake.count("sync/copy"); got != 2 {
		t.Fatalf("sync/copy calls = %d, want 2", got)
	}
	if got := fake.count("operations/stat"); got != 2 {
		t.Fatalf("operations/stat calls = %d, want 2 (start + resume)", got)
	}
	call := fake.inputFor(t, "sync/copy")
	filter, ok := call["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("resume filter missing: %#v", call)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if !reflect.DeepEqual(rules, []string{"/Фото/**"}) {
		t.Fatalf("rules = %#v", rules)
	}
}

func TestResumeRefusesMissingSource(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddAccount(store.Account{ID: "a1", Remote: "cf_a1"}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	job, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Paths: []string{"Фото"}}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := st.UpdateJob(job.ID, func(j *store.Job) bool {
		j.Status = store.StatusInterrupted
		return true
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	fake.responses["operations/stat"] = map[string]any{"item": nil}

	_, err = manager.Resume(context.Background(), job.ID)
	if !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("err = %v, want ErrSourceNotFound", err)
	}
	var notFound *SourceNotFoundError
	if !errors.As(err, &notFound) || notFound.Path != "Фото" {
		t.Fatalf("error does not name the folder: %v", err)
	}
	stored, _ := st.Job(job.ID)
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("job was claimed before verification: %q", stored.Status)
	}
	if got := fake.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", got)
	}
}

func TestStartMissingSourceReturnsErrSourceNotFound(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.responses["operations/stat"] = map[string]any{"item": nil}

	_, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Paths: []string{"Пропавшая"}}, "/downloads/yandex", "copy")
	if !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("err = %v, want ErrSourceNotFound", err)
	}
	var notFound *SourceNotFoundError
	if !errors.As(err, &notFound) || notFound.Path != "Пропавшая" {
		t.Fatalf("error does not name the folder: %v", err)
	}
	if jobs := st.Jobs(); len(jobs) != 0 {
		t.Fatalf("job persisted despite a bad source: %+v", jobs)
	}
	if got := fake.count("sync/copy"); got != 0 {
		t.Fatalf("sync/copy calls = %d, want 0", got)
	}
}

func TestStartSourceIsFile(t *testing.T) {
	manager, _, st := newTestManager(t)
	fake := &fakeRC{responses: map[string]any{
		"operations/stat": map[string]any{"item": map[string]any{"IsDir": false}},
	}}
	manager.rc = fake

	_, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Paths: []string{"док.txt"}}, "/downloads/yandex", "copy")
	if !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("err = %v, want ErrSourceNotFound", err)
	}
	if jobs := st.Jobs(); len(jobs) != 0 {
		t.Fatalf("job persisted despite a file source: %+v", jobs)
	}
}

func TestVerifySourcesTreatsRcloneFailureAsOutage(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.errors = map[string]error{"operations/stat": errors.New("dial unix: connection refused")}

	_, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Paths: []string{"Фото"}}, "/downloads/yandex", "copy")
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("network failure must not look like a missing folder: %v", err)
	}
	if jobs := st.Jobs(); len(jobs) != 0 {
		t.Fatalf("job persisted during an outage: %+v", jobs)
	}
}

func TestVerifySourcesMapsNotFoundAPIError(t *testing.T) {
	manager, fake, _ := newTestManager(t)
	fake.errors = map[string]error{"operations/stat": &rc.APIError{Method: "operations/stat", Status: 500, Body: "directory not found"}}

	_, err := manager.Start(context.Background(), store.Account{ID: "a1", Remote: "cf_a1"}, Source{Paths: []string{"Фото"}}, "/downloads/yandex", "copy")
	if !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("err = %v, want ErrSourceNotFound", err)
	}
}

func TestDestConflictCoversNesting(t *testing.T) {
	cases := []struct {
		name     string
		first    string
		second   string
		conflict bool
	}{
		{"same", "/downloads/a", "/downloads/a", true},
		{"child", "/downloads/a", "/downloads/a/b", true},
		{"new dest is a parent of active", "/downloads/a/b", "/downloads/a", true},
		{"new dest is a grandparent of active", "/downloads/a/b/c", "/downloads/a", true},
		{"sibling prefix", "/downloads/a", "/downloads/ab", false},
		{"different", "/downloads/a", "/downloads/other", false},
		{"sibling deep", "/downloads/a/b", "/downloads/a/c", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager, _, _ := newTestManager(t)
			account := store.Account{ID: "a1", Remote: "cf_a1"}
			if _, err := manager.Start(context.Background(), account, Source{}, tc.first, "copy"); err != nil {
				t.Fatalf("first start: %v", err)
			}
			_, err := manager.Start(context.Background(), account, Source{}, tc.second, "copy")
			if tc.conflict && !errors.Is(err, ErrConflict) {
				t.Fatalf("err = %v, want ErrConflict", err)
			}
			if !tc.conflict && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCheckUsesFilterForMultiJob(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusDone, Source: "cf_a1:",
		SourcePaths: []string{"A", "B/sub"}, Dest: "/downloads/yandex",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	if _, err := manager.Check(context.Background(), "j1"); err != nil {
		t.Fatalf("check: %v", err)
	}
	call := fake.inputFor(t, "operations/check")
	if call["srcFs"] != "cf_a1:" {
		t.Fatalf("srcFs = %v", call["srcFs"])
	}
	filter, ok := call["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if !reflect.DeepEqual(rules, []string{"/A/**", "/B/sub/**"}) {
		t.Fatalf("rules = %#v", rules)
	}
}

func TestCheckLegacyJobHasNoFilter(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddJob(store.Job{ID: "j1", Status: store.StatusDone, Source: "cf_a1:/фото", Dest: "/downloads/yandex"}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	if _, err := manager.Check(context.Background(), "j1"); err != nil {
		t.Fatalf("check: %v", err)
	}
	call := fake.inputFor(t, "operations/check")
	if call["srcFs"] != "cf_a1:/фото" {
		t.Fatalf("srcFs = %v", call["srcFs"])
	}
	if _, exists := call["_filter"]; exists {
		t.Fatalf("legacy check must not carry a filter: %#v", call)
	}
}

func TestCheckWholeDiskJobHasNoFilter(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddJob(store.Job{ID: "j1", Status: store.StatusDone, Source: "cf_a1:", Dest: "/downloads/yandex"}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	if _, err := manager.Check(context.Background(), "j1"); err != nil {
		t.Fatalf("check: %v", err)
	}
	call := fake.inputFor(t, "operations/check")
	if call["srcFs"] != "cf_a1:" {
		t.Fatalf("srcFs = %v", call["srcFs"])
	}
	if _, exists := call["_filter"]; exists {
		t.Fatalf("whole-disk check must not carry a filter: %#v", call)
	}
}

func TestResumeRefusesMissingLegacySource(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddJob(store.Job{
		ID: "legacy", Status: store.StatusInterrupted, Source: "cf_a1:/Фото",
		Dest: "/downloads/yandex", Group: "g", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	fake.responses["operations/stat"] = map[string]any{"item": nil}

	_, err := manager.Resume(context.Background(), "legacy")
	if !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("err = %v, want ErrSourceNotFound", err)
	}
	var notFound *SourceNotFoundError
	if !errors.As(err, &notFound) || notFound.Path != "Фото" {
		t.Fatalf("error does not name the folder: %v", err)
	}
	if stored, _ := st.Job("legacy"); stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted", stored.Status)
	}
	if got := fake.count("sync/copy"); got != 0 {
		t.Fatalf("sync/copy calls = %d, want 0", got)
	}
}

func TestResumeResetsStartedAt(t *testing.T) {
	manager, _, st := newTestManager(t)
	oldStarted := time.Now().Add(-time.Hour).UTC()
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusInterrupted, Source: "cf_a1:",
		Dest: "/downloads/yandex", Group: "g", CreatedAt: oldStarted, StartedAt: oldStarted,
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	before := time.Now().UTC()
	resumed, err := manager.Resume(context.Background(), "j1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !resumed.StartedAt.After(oldStarted) || resumed.StartedAt.Before(before.Add(-time.Second)) {
		t.Fatalf("startedAt = %v, want the resume time (old %v)", resumed.StartedAt, oldStarted)
	}
	if !resumed.FinishedAt.IsZero() {
		t.Fatalf("finishedAt = %v, want zero", resumed.FinishedAt)
	}
}

func TestPollFinalisesFailedJobWithEngineErrorPayload(t *testing.T) {
	manager, fake, st := newTestManager(t)
	// rclone reports a finished transfer that failed: success=false with the
	// reason in the top-level "error" field. That is data, not a broken rc call,
	// so the job must become failed instead of hanging in "running" for ever.
	fake.responses = map[string]any{"job/status": map[string]any{
		"finished": true,
		"success":  false,
		"error":    "failed to open source object: 503 Service Unavailable",
	}}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, ExecuteID: "exec-1",
		Group: "migrate/j1", Source: "cf_a1:", Dest: "/downloads/yandex",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	manager.Poll(context.Background())

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusFailed {
		t.Fatalf("status = %q, want failed", stored.Status)
	}
	if !strings.Contains(stored.Error, "503") {
		t.Fatalf("error = %q, want the engine reason", stored.Error)
	}
}

func TestPollInterruptsJobAfterStatusErrorGrace(t *testing.T) {
	manager, fake, st := newTestManager(t)
	manager.opts.StatusErrorGrace = 0
	// rclone answers with a non-job error for ever. After the grace period the
	// record must become interruptible instead of hanging in "running".
	fake.errors = map[string]error{"job/status": &rc.APIError{Method: "job/status", Status: http.StatusInternalServerError, Body: "internal error"}}
	if err := st.AddJob(store.Job{
		ID: "j1", Status: store.StatusRunning, RcloneJobID: 7, Group: "migrate/j1",
		Source: "cf_a1:", Dest: "/downloads/yandex",
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	manager.Poll(context.Background())
	manager.Poll(context.Background())

	stored, _ := st.Job("j1")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted", stored.Status)
	}
	if fake.count("job/stop") != 1 {
		t.Fatalf("job/stop calls = %d, want 1", fake.count("job/stop"))
	}
}
