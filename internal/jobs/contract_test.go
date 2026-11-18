package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloudferry/internal/disk"
	"cloudferry/internal/store"
)

// Two simultaneous starts with different idempotency keys must resolve to one
// job: the key lookup cannot replay across different keys, so only claimDest
// keeps the destination single-writer. Both goroutines pass cloud verification
// before either claims the destination, so the test enters the claim with the
// widest possible race window.
func TestConcurrentStartDifferentKeysOneDestConflict(t *testing.T) {
	manager, fake, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}

	var (
		verifiedMu sync.Mutex
		verified   int
		bothReady  = make(chan struct{})
		closeOnce  sync.Once
		barrierOut atomic.Bool
	)
	fake.onCall = func(method string) {
		if method != "operations/stat" {
			return
		}
		verifiedMu.Lock()
		verified++
		reached := verified == 2
		verifiedMu.Unlock()
		if reached {
			closeOnce.Do(func() { close(bothReady) })
		}
		select {
		case <-bothReady:
		case <-time.After(5 * time.Second):
			barrierOut.Store(true)
		}
	}

	type attempt struct {
		job store.Job
		err error
	}
	attempts := make(chan attempt, 2)
	start := func(key string) {
		job, _, err := manager.StartIdempotent(context.Background(), account, Source{Path: "Фото"}, "/downloads/race", "copy", key)
		attempts <- attempt{job: job, err: err}
	}
	go start("race-key-one")
	go start("race-key-two")

	created, conflicted := 0, 0
	for i := 0; i < 2; i++ {
		got := <-attempts
		switch {
		case got.err == nil:
			created++
			if got.job.ID == "" || got.job.Status != store.StatusRunning {
				t.Fatalf("created job = %+v, want a running download", got.job)
			}
			if got.job.IdempotencyKey == "" {
				t.Fatalf("created job kept no key: %+v", got.job)
			}
		case errors.Is(got.err, ErrConflict):
			conflicted++
		default:
			t.Fatalf("unexpected start error: %v", got.err)
		}
	}
	if barrierOut.Load() {
		t.Fatal("the verification barrier timed out: both starts must reach the claim")
	}
	if created != 1 || conflicted != 1 {
		t.Fatalf("created = %d, conflict = %d, want one job and one conflict", created, conflicted)
	}

	jobs := st.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want one record", len(jobs))
	}
	if jobs[0].Dest != "/downloads/race" {
		t.Fatalf("dest = %q", jobs[0].Dest)
	}
	overlapping := 0
	for _, job := range jobs {
		active := job.Status == store.StatusQueued || job.Status == store.StatusRunning
		if active && destOverlapsActive("/downloads/race", job.Dest) {
			overlapping++
		}
	}
	if overlapping != 1 {
		t.Fatalf("active jobs on the destination = %d, want 1", overlapping)
	}
	if got := fake.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls = %d, want 1: the losing start must not launch", got)
	}
}

// Stop can land between the queued check and the engine answer. The record is
// already canceled then, so launchQueued must refuse it and stop the fresh
// engine job instead of leaving an orphan writing into an unwatched folder.
func TestStopWhileLaunchInFlightStopsOrphanTransfer(t *testing.T) {
	manager, fake, st := newTestManager(t)
	queued := store.Job{
		ID: "j1", Status: store.StatusQueued, Source: "cf_a1:",
		Dest: "/downloads/x", Group: "migrate/j1", CreatedAt: time.Now().UTC(),
	}
	if err := st.AddJob(queued); err != nil {
		t.Fatalf("add job: %v", err)
	}

	var stopErr error
	fake.onCall = func(method string) {
		if method == "sync/copy" {
			// The engine call is already in flight; the user stops the download.
			_, stopErr = manager.Stop(context.Background(), queued.ID)
		}
	}

	err := manager.launchQueued(context.Background(), queued)
	if !errors.Is(err, errLaunchWindowLost) {
		t.Fatalf("launchQueued error = %v, want errLaunchWindowLost", err)
	}
	if stopErr != nil {
		t.Fatalf("stop during launch: %v", stopErr)
	}
	stored, ok := st.Job(queued.ID)
	if !ok {
		t.Fatal("job disappeared")
	}
	if stored.Status != store.StatusCanceled || stored.RcloneJobID != 0 {
		t.Fatalf("stored job = %+v, want canceled without an engine id", stored)
	}
	if got := fake.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", got)
	}
	if got := fake.count("job/stop"); got != 1 {
		t.Fatalf("job/stop calls = %d, want the orphan engine job stopped", got)
	}
}

// The transfer limit is only useful when the launch request actually carries it:
// the review regression covered the formula, this pins the wiring into _config.
// With a measurable destination the limit is always set, even though the formula
// test covers the sub-gibibyte case that used to disable it.
func TestLaunchConfigCarriesTransferLimit(t *testing.T) {
	manager, fake, st := newTestManager(t)
	dest := t.TempDir()
	queued := store.Job{
		ID: "j1", Status: store.StatusQueued, Source: "cf_a1:",
		Dest: dest, Group: "migrate/j1", CreatedAt: time.Now().UTC(),
	}
	if err := st.AddJob(queued); err != nil {
		t.Fatalf("add job: %v", err)
	}

	if err := manager.launchQueued(context.Background(), queued); err != nil {
		t.Fatalf("launch queued job: %v", err)
	}

	call := fake.inputFor(t, "sync/copy")
	config, ok := call["_config"].(map[string]any)
	if !ok {
		t.Fatalf("_config missing from the launch request: %#v", call)
	}
	free, err := disk.Free(dest)
	if err != nil {
		t.Fatalf("free space of %s: %v", dest, err)
	}
	want, ok := transferLimitForFree(free)
	if !ok {
		t.Fatalf("transferLimitForFree(%d) reported no limit", free)
	}
	if got := config["MaxTransfer"]; got != want {
		t.Fatalf("MaxTransfer = %v, want %q for %d free bytes", got, want, free)
	}
}
