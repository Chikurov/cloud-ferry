package jobs

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

// A launch that never reached rclone leaves no transfer to replay: a retry with
// the same client key must start the download again instead of answering 200
// with the failed record.
func TestReviewStartRetriesAfterLaunchFailure(t *testing.T) {
	manager, fake, st := newTestManager(t)
	fake.errors = map[string]error{"sync/copy": errors.New("rclone refused the transfer")}
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	start := func() (store.Job, bool, error) {
		return manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/x", "copy", "retry-key-1")
	}

	first, replayed, err := start()
	if err == nil || replayed {
		t.Fatalf("first start: err = %v, replayed = %v, want a real failure", err, replayed)
	}
	stored, ok := st.Job(first.ID)
	if !ok || stored.Status != store.StatusFailed {
		t.Fatalf("stored job = %+v (found %v), want failed", stored, ok)
	}
	if stored.IdempotencyKey != "" {
		t.Fatalf("a failed launch kept key %q: a retry would replay an empty job", stored.IdempotencyKey)
	}

	second, replayed, err := start()
	if err == nil || replayed {
		t.Fatalf("retry: err = %v, replayed = %v, want a second attempt", err, replayed)
	}
	if second.ID == first.ID {
		t.Fatalf("retry returned the failed job %q instead of starting again", second.ID)
	}
	if calls := fake.count("sync/copy"); calls != 2 {
		t.Fatalf("sync/copy calls = %d, want 2", calls)
	}
}

// A stored failed record without an engine job (older state file or a lost
// race) is never a valid replay answer either.
func TestReviewStoredFailedJobWithoutTransferIsNotReplayed(t *testing.T) {
	manager, fake, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	if err := st.AddJob(store.Job{
		ID: "old-failure", AccountID: account.ID, IdempotencyKey: "legacy-key-1",
		Source: "cf_a1:/фото", Dest: "/downloads/x", Mode: "copy",
		Status: store.StatusFailed, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add failed job: %v", err)
	}

	job, replayed, err := manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/x", "copy", "legacy-key-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if replayed || job.ID == "old-failure" {
		t.Fatalf("replayed = %v, job = %q: a transfer that never started must not be replayed", replayed, job.ID)
	}
	if calls := fake.count("sync/copy"); calls != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", calls)
	}
}

// The error grace period belongs to one attempt. "Продолжить" must clear it, so
// the first engine error after the resume does not interrupt the new attempt
// immediately.
func TestReviewResumeResetsStatusErrorGrace(t *testing.T) {
	manager, fake, st := newTestManager(t)
	manager.opts.StatusErrorGrace = 20 * time.Millisecond
	account := store.Account{ID: "a1", Remote: "cf_a1"}

	job, err := manager.Start(context.Background(), account, Source{}, "/downloads/x", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	statusError := &rc.APIError{Method: "job/status", Status: http.StatusInternalServerError, Body: "internal error"}
	fake.errors = map[string]error{"job/status": statusError}

	// The first error starts the grace timer but does not interrupt yet.
	manager.Poll(context.Background())
	if stored, _ := st.Job(job.ID); stored.Status != store.StatusRunning {
		t.Fatalf("status = %q after the first error, want running", stored.Status)
	}
	time.Sleep(30 * time.Millisecond)
	manager.Poll(context.Background())
	if stored, _ := st.Job(job.ID); stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q after the grace period, want interrupted", stored.Status)
	}
	// Interrupting already drops the timer: the next attempt must not inherit it.
	manager.mu.Lock()
	_, pending := manager.statusErrorSince[job.ID]
	manager.mu.Unlock()
	if pending {
		t.Fatalf("interrupted job %q kept its status-error timer", job.ID)
	}

	delete(fake.errors, "job/status")
	resumed, err := manager.Resume(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != store.StatusRunning {
		t.Fatalf("status after resume = %q, want running", resumed.Status)
	}
	manager.mu.Lock()
	_, carried := manager.statusErrorSince[job.ID]
	manager.mu.Unlock()
	if carried {
		t.Fatalf("resumed job %q inherited the previous attempt's status-error timer", job.ID)
	}

	fake.errors = map[string]error{"job/status": statusError}
	manager.Poll(context.Background())
	if stored, _ := st.Job(job.ID); stored.Status != store.StatusRunning {
		t.Fatalf("status = %q after one error following resume, want running", stored.Status)
	}
}

// markInterrupted and finalize are the two other places a job stops being
// polled; both must release the per-job error state instead of leaking it.
func TestReviewStatusErrorStateClearedOnInterruptAndFinalize(t *testing.T) {
	t.Run("interrupt", func(t *testing.T) {
		manager, _, st := newTestManager(t)
		if err := st.AddJob(store.Job{ID: "j1", Status: store.StatusRunning, RcloneJobID: 7}); err != nil {
			t.Fatalf("add job: %v", err)
		}
		manager.statusErrorLongEnough("j1")

		manager.markInterrupted("j1", "test")

		manager.mu.Lock()
		_, since := manager.statusErrorSince["j1"]
		errors := manager.statusErrors["j1"]
		manager.mu.Unlock()
		if since || errors != 0 {
			t.Fatalf("grace state = (%v, %d), want cleared", since, errors)
		}
	})

	t.Run("finalize", func(t *testing.T) {
		manager, fake, st := newTestManager(t)
		fake.responses["job/status"] = map[string]any{"finished": true, "success": true}
		if err := st.AddJob(store.Job{ID: "j1", Status: store.StatusRunning, RcloneJobID: 7}); err != nil {
			t.Fatalf("add job: %v", err)
		}
		manager.statusErrorLongEnough("j1")

		if err := manager.Recover(); err != nil {
			t.Fatalf("recover: %v", err)
		}

		if stored, _ := st.Job("j1"); stored.Status != store.StatusDone {
			t.Fatalf("status = %q, want done", stored.Status)
		}
		manager.mu.Lock()
		_, since := manager.statusErrorSince["j1"]
		manager.mu.Unlock()
		if since {
			t.Fatal("a finalized job kept its status-error timer")
		}
	})
}

// Stop may cancel a queued record between its creation and the launch call.
// The launch must re-read the status under createMu and not start the transfer.
func TestReviewStopBeforeLaunchPreventsTransfer(t *testing.T) {
	manager, fake, st := newTestManager(t)
	queued := store.Job{
		ID: "j1", Status: store.StatusQueued, Source: "cf_a1:",
		Dest: "/downloads/x", Group: "migrate/j1", CreatedAt: time.Now().UTC(),
	}
	if err := st.AddJob(queued); err != nil {
		t.Fatalf("add job: %v", err)
	}
	if _, err := manager.Stop(context.Background(), "j1"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if err := manager.launchQueued(context.Background(), queued); !errors.Is(err, errJobNotQueued) {
		t.Fatalf("launchQueued error = %v, want errJobNotQueued", err)
	}
	if calls := fake.count("sync/copy"); calls != 0 {
		t.Fatalf("sync/copy calls = %d, want 0 for a stopped job", calls)
	}
	if stored, _ := st.Job("j1"); stored.Status != store.StatusCanceled {
		t.Fatalf("status = %q, want canceled", stored.Status)
	}

	// A record that is still queued must launch normally.
	live := store.Job{
		ID: "j2", Status: store.StatusQueued, Source: "cf_a1:",
		Dest: "/downloads/y", Group: "migrate/j2", CreatedAt: time.Now().UTC(),
	}
	if err := st.AddJob(live); err != nil {
		t.Fatalf("add live job: %v", err)
	}
	if err := manager.launchQueued(context.Background(), live); err != nil {
		t.Fatalf("launch queued job: %v", err)
	}
	if calls := fake.count("sync/copy"); calls != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", calls)
	}
	if stored, _ := st.Job("j2"); stored.Status != store.StatusRunning || stored.RcloneJobID != 7 {
		t.Fatalf("live job = %+v, want running with engine id 7", stored)
	}
}

// Even a small amount of free space must produce a max-transfer limit; the old
// code only did so above 1 GiB and let rclone fill the rest of the disk.
func TestReviewTransferLimitForSmallFreeSpace(t *testing.T) {
	cases := []struct {
		name string
		free uint64
		want string
		ok   bool
	}{
		{"unknown free space", 0, "", false},
		{"below one gibibyte", 512 << 20, "460M", true},
		{"below one mebibyte", 2 << 20, "1M", true},
		{"below one kibibyte", 500, "450B", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := transferLimitForFree(tc.free)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("transferLimitForFree(%d) = (%q, %v), want (%q, %v)", tc.free, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// Check is meaningful only for a finished attempt and runs at most once per
// attempt; Resume clears the stored result so the next attempt can be checked.
func TestReviewCheckOnlyForFinishedJobsAndOnlyOnce(t *testing.T) {
	manager, fake, st := newTestManager(t)
	ctx := context.Background()

	for _, status := range []string{store.StatusRunning, store.StatusQueued, store.StatusCanceled} {
		id := "job-" + status
		if err := st.AddJob(store.Job{ID: id, Status: status, Source: "cf_a1:", Dest: "/downloads/a"}); err != nil {
			t.Fatalf("add %s job: %v", status, err)
		}
		if _, err := manager.Check(ctx, id); !errors.Is(err, ErrConflict) {
			t.Fatalf("check on %q: err = %v, want ErrConflict", status, err)
		}
	}
	if calls := fake.count("operations/check"); calls != 0 {
		t.Fatalf("operations/check calls = %d, want 0 for unfinished jobs", calls)
	}

	if err := st.AddJob(store.Job{ID: "done", Status: store.StatusDone, Source: "cf_a1:", Dest: "/downloads/b"}); err != nil {
		t.Fatalf("add done job: %v", err)
	}
	result, err := manager.Check(ctx, "done")
	if err != nil {
		t.Fatalf("check done: %v", err)
	}
	if result.Differ != 1 {
		t.Fatalf("result = %+v, want one differing file", result)
	}
	if _, err := manager.Check(ctx, "done"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second check: err = %v, want ErrConflict", err)
	}
	if calls := fake.count("operations/check"); calls != 1 {
		t.Fatalf("operations/check calls = %d, want 1", calls)
	}

	if err := st.AddJob(store.Job{ID: "paused", Status: store.StatusInterrupted, Source: "cf_a1:", Dest: "/downloads/c"}); err != nil {
		t.Fatalf("add interrupted job: %v", err)
	}
	if _, err := manager.Check(ctx, "paused"); err != nil {
		t.Fatalf("check interrupted: %v", err)
	}
}

// Two clicks in one moment must not run two comparisons of the same tree.
func TestReviewConcurrentChecksRunOnce(t *testing.T) {
	manager, fake, st := newTestManager(t)
	if err := st.AddJob(store.Job{ID: "j1", Status: store.StatusDone, Source: "cf_a1:", Dest: "/downloads/a"}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fake.onCall = func(method string) {
		if method != "operations/check" {
			return
		}
		once.Do(func() { close(started) })
		<-release
	}

	first := make(chan error, 1)
	go func() {
		_, err := manager.Check(context.Background(), "j1")
		first <- err
	}()
	<-started

	if _, err := manager.Check(context.Background(), "j1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("second concurrent check: err = %v, want ErrConflict", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first check: %v", err)
	}
	if calls := fake.count("operations/check"); calls != 1 {
		t.Fatalf("operations/check calls = %d, want 1", calls)
	}
}
