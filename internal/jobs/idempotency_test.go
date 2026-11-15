package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloudferry/internal/store"
)

func TestStartIdempotentReplaysSameKey(t *testing.T) {
	manager, fake, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}

	first, replayed, err := manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/x", "copy", "key-0001")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if replayed {
		t.Fatal("the first request must not be a replay")
	}
	if first.IdempotencyKey != "key-0001" {
		t.Fatalf("idempotency key = %q, want key-0001", first.IdempotencyKey)
	}
	if first.Status != store.StatusRunning {
		t.Fatalf("status = %q, want running", first.Status)
	}

	second, replayed, err := manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/x", "copy", "key-0001")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed {
		t.Fatal("the retry must be reported as a replay")
	}
	if second.ID != first.ID {
		t.Fatalf("replay job = %q, want %q", second.ID, first.ID)
	}
	if jobs := st.Jobs(); len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if calls := fake.count("sync/copy"); calls != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", calls)
	}
}

func TestStartIdempotentReplaysMultiSource(t *testing.T) {
	manager, fake, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	fake.setStat("a.txt", map[string]any{"IsDir": false})
	src := Source{Paths: []string{"Фото", "Документы"}, Files: []string{"a.txt"}}

	first, _, err := manager.StartIdempotent(context.Background(), account, src, "/downloads/multi", "copy", "multi-key-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	second, replayed, err := manager.StartIdempotent(context.Background(), account, src, "/downloads/multi", "copy", "multi-key-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed || second.ID != first.ID {
		t.Fatalf("replayed = %v, id = %q, want %q", replayed, second.ID, first.ID)
	}
	if jobs := st.Jobs(); len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
}

func TestStartIdempotentConflictsOnOtherParameters(t *testing.T) {
	cases := []struct {
		name    string
		src     Source
		dstPath string
		mode    string
	}{
		{"other destination", Source{Path: "фото"}, "/downloads/other", "copy"},
		{"other source", Source{Path: "документы"}, "/downloads/x", "copy"},
		{"other files", Source{Path: "фото", Files: []string{"a.txt"}}, "/downloads/x", "copy"},
		{"other mode", Source{Path: "фото"}, "/downloads/x", "sync"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager, fake, st := newTestManager(t)
			account := store.Account{ID: "a1", Remote: "cf_a1"}
			if _, _, err := manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/x", "copy", "key-conflict-1"); err != nil {
				t.Fatalf("start: %v", err)
			}

			_, replayed, err := manager.StartIdempotent(context.Background(), account, tc.src, tc.dstPath, tc.mode, "key-conflict-1")
			if !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
			}
			if replayed {
				t.Fatal("a conflict must not be reported as a replay")
			}
			if jobs := st.Jobs(); len(jobs) != 1 {
				t.Fatalf("jobs = %d, want 1", len(jobs))
			}
			if calls := fake.count("sync/copy") + fake.count("sync/sync"); calls != 1 {
				t.Fatalf("transfer calls = %d, want 1", calls)
			}
		})
	}
}

// TestStartIdempotentKeyIsPerAccount: the same key on another account is a new
// request, because jobs never cross accounts.
func TestStartIdempotentKeyIsPerAccount(t *testing.T) {
	manager, _, st := newTestManager(t)
	first := store.Account{ID: "a1", Remote: "cf_a1"}
	second := store.Account{ID: "a2", Remote: "cf_a2"}

	if _, _, err := manager.StartIdempotent(context.Background(), first, Source{Path: "фото"}, "/downloads/x", "copy", "shared-key-1"); err != nil {
		t.Fatalf("first account: %v", err)
	}
	job, replayed, err := manager.StartIdempotent(context.Background(), second, Source{Path: "фото"}, "/downloads/y", "copy", "shared-key-1")
	if err != nil {
		t.Fatalf("second account: %v", err)
	}
	if replayed {
		t.Fatal("another account must not replay a foreign job")
	}
	if job.AccountID != second.ID {
		t.Fatalf("account = %q, want %q", job.AccountID, second.ID)
	}
	if jobs := st.Jobs(); len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
}

// TestStartIdempotentForgetsOldKey: after the window the key starts a fresh
// download instead of answering with a day-old job.
func TestStartIdempotentForgetsOldKey(t *testing.T) {
	manager, _, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	old := store.Job{
		ID: "old", AccountID: account.ID, IdempotencyKey: "expired-key-1",
		Source: "cf_a1:/фото", Dest: "/downloads/x", Mode: "copy", Status: store.StatusDone,
		CreatedAt: time.Now().Add(-25 * time.Hour),
	}
	if err := st.AddJob(old); err != nil {
		t.Fatalf("add old job: %v", err)
	}

	job, replayed, err := manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/x", "copy", "expired-key-1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if replayed {
		t.Fatal("an expired key must not replay the old job")
	}
	if job.ID == old.ID {
		t.Fatal("an expired key must create a new job")
	}
	if jobs := st.Jobs(); len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
}

// TestStartIdempotentReplaysLegacySourceJob: the source of a stored job is
// compared in its recorded form, so a job written by an older build with only
// Source still replays a matching request.
func TestStartIdempotentReplaysLegacySourceJob(t *testing.T) {
	manager, fake, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	old := store.Job{
		ID: "legacy", AccountID: account.ID, IdempotencyKey: "legacy-key-1",
		Source: "cf_a1:/фото", Dest: "/downloads/x", Mode: "copy", Status: store.StatusDone,
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	if err := st.AddJob(old); err != nil {
		t.Fatalf("add job: %v", err)
	}

	job, replayed, err := manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/x", "copy", "legacy-key-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed || job.ID != old.ID {
		t.Fatalf("replayed = %v, id = %q, want %q", replayed, job.ID, old.ID)
	}
	if calls := fake.count("sync/copy"); calls != 0 {
		t.Fatalf("a replay must not launch a transfer: %d calls", calls)
	}
}

func TestStartIdempotentReplayDoesNotTouchCloud(t *testing.T) {
	manager, fake, _ := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	if _, _, err := manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/x", "copy", "offline-key-1"); err != nil {
		t.Fatalf("start: %v", err)
	}
	callsBefore := fake.count("operations/stat")
	fake.errors = map[string]error{"operations/stat": errors.New("provider is down")}

	job, replayed, err := manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/x", "copy", "offline-key-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed || job.ID == "" {
		t.Fatalf("replayed = %v, job = %+v", replayed, job)
	}
	if calls := fake.count("operations/stat"); calls != callsBefore {
		t.Fatalf("a replay must not verify the cloud: %d extra calls", calls-callsBefore)
	}
}

// TestStartWithoutKeySkipsIdempotency: without a key nothing is deduplicated,
// and the stored job carries no key.
func TestStartWithoutKeySkipsIdempotency(t *testing.T) {
	manager, _, st := newTestManager(t)
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	first, err := manager.Start(context.Background(), account, Source{Path: "фото"}, "/downloads/one", "copy")
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	second, err := manager.Start(context.Background(), account, Source{Path: "фото"}, "/downloads/two", "copy")
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("jobs without a key must be independent")
	}
	third, replayed, err := manager.StartIdempotent(context.Background(), account, Source{Path: "фото"}, "/downloads/three", "copy", "")
	if err != nil || replayed {
		t.Fatalf("empty key: err = %v, replayed = %v", err, replayed)
	}
	if third.ID == "" {
		t.Fatal("an empty key must still create a job")
	}
	for _, job := range st.Jobs() {
		if job.IdempotencyKey != "" {
			t.Fatalf("job %q got key %q without one in the request", job.ID, job.IdempotencyKey)
		}
	}
}
