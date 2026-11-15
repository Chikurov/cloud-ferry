package store

import (
	"fmt"
	"testing"
	"time"
)

// Pruning must not forget a job whose idempotency key can still be replayed:
// the retry would otherwise start a second transfer. A key older than the
// window no longer protects its record.
func TestReviewPruneKeepsLiveIdempotencyKeys(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	now := time.Now()
	for i := 0; i < 150; i++ {
		job := Job{
			ID:        fmt.Sprintf("j%03d", i),
			Status:    StatusDone,
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}
		if i < 2 {
			job.IdempotencyKey = fmt.Sprintf("live-key-%d", i)
		}
		if err := st.AddJob(job); err != nil {
			t.Fatalf("add job %d: %v", i, err)
		}
	}

	for _, id := range []string{"j000", "j001"} {
		if _, ok := st.Job(id); !ok {
			t.Fatalf("job %q with a live idempotency key was pruned", id)
		}
	}
	if _, ok := st.Job("j002"); ok {
		t.Fatal("an ordinary old job must still be pruned")
	}
	if jobs := st.Jobs(); len(jobs) != maxJobs {
		t.Fatalf("jobs = %d, want %d (two protected keys plus the newest history)", len(jobs), maxJobs)
	}

	expired := Job{
		ID:             "expired",
		Status:         StatusDone,
		IdempotencyKey: "expired-key-1",
		CreatedAt:      now.Add(-25 * time.Hour),
	}
	if err := st.AddJob(expired); err != nil {
		t.Fatalf("add expired job: %v", err)
	}
	if err := st.AddJob(Job{ID: "newest", Status: StatusDone, CreatedAt: now.Add(200 * time.Second)}); err != nil {
		t.Fatalf("add newest job: %v", err)
	}
	if _, ok := st.Job("expired"); ok {
		t.Fatal("a key older than the idempotency window must not protect the record")
	}
}
