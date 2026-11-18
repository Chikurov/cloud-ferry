package store

import (
	"fmt"
	"testing"
	"time"
)

// Pruning must never evict a queued, running or verifying job: the transfer is
// still in progress, so its record cannot be dropped even when it is the oldest
// item or carries an idempotency key that is already too old to replay.
func TestPruneJobsKeepsActiveJobsOverLimit(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	now := time.Now()
	for i := 0; i < maxJobs; i++ {
		if err := st.AddJob(Job{
			ID: fmt.Sprintf("done-%03d", i), Status: StatusDone,
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("add done job %d: %v", i, err)
		}
	}

	active := []Job{
		{ID: "running-old-key", Status: StatusRunning, IdempotencyKey: "expired-key-1", CreatedAt: now.Add(-25 * time.Hour)},
		{ID: "queued", Status: StatusQueued, CreatedAt: now},
		{ID: "verifying", Status: StatusVerifying, CreatedAt: now},
	}
	for _, job := range active {
		if err := st.AddJob(job); err != nil {
			t.Fatalf("add active job %s: %v", job.ID, err)
		}
	}

	for _, job := range active {
		if _, ok := st.Job(job.ID); !ok {
			t.Fatalf("active job %q was pruned", job.ID)
		}
	}
	if jobs := st.Jobs(); len(jobs) != maxJobs {
		t.Fatalf("jobs = %d, want %d (active records kept, finished history trimmed)", len(jobs), maxJobs)
	}
	if _, ok := st.Job("done-000"); ok {
		t.Fatal("an ordinary old finished job must still be pruned")
	}
}
