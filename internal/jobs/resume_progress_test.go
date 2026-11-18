package jobs

import (
	"context"
	"testing"

	"cloudferry/internal/store"
)

// A resumed attempt must not lose the progress of the previous one. rclone
// counts each engine job separately and reports only the remainder, so the
// backend keeps the bytes transferred before the pause and adds them to every
// poll; the planned total never shrinks either.
func TestResumeKeepsProgressAndCountsCumulatively(t *testing.T) {
	manager, fake, st := newTestManager(t)
	ctx := context.Background()
	account := store.Account{ID: "a1", Remote: "cf_a1"}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	job, err := manager.Start(ctx, account, Source{}, "/downloads/yandex", "copy")
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// First attempt: 40 % of 1000 bytes.
	fake.responses["job/status"] = map[string]any{"finished": false}
	fake.responses["core/stats"] = map[string]any{
		"bytes": 400, "totalBytes": 1000, "speed": 40, "eta": 600,
		"transfers": 4, "totalTransfers": 10, "elapsedTime": 60,
	}
	manager.Poll(ctx)
	stored, _ := st.Job(job.ID)
	if stored.Stats.Bytes != 400 || stored.Stats.TotalBytes != 1000 {
		t.Fatalf("first attempt stats = %+v", stored.Stats)
	}

	// Pause, then continue: the old engine job is gone.
	if _, err := manager.Stop(ctx, job.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	fake.responses["job/status"] = map[string]any{"finished": true, "success": true}
	resumed, err := manager.Resume(ctx, job.ID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.BytesBase != 400 {
		t.Fatalf("bytesBase = %d, want 400", resumed.BytesBase)
	}
	if resumed.Stats.Bytes != 400 || resumed.Stats.TotalBytes != 1000 {
		t.Fatalf("progress was reset on resume: %+v", resumed.Stats)
	}
	if resumed.Stats.Speed != 0 || resumed.Stats.ETA != 0 || resumed.Stats.Errors != 0 || resumed.Stats.ElapsedTime != 0 {
		t.Fatalf("live stats were not cleared on resume: %+v", resumed.Stats)
	}

	// The resumed engine job counts only the remainder: 100 of 300 bytes.
	fake.responses["job/status"] = map[string]any{"finished": false}
	fake.responses["core/stats"] = map[string]any{
		"bytes": 100, "totalBytes": 300, "speed": 10,
		"transfers": 1, "totalTransfers": 3, "elapsedTime": 20,
	}
	manager.Poll(ctx)
	stored, _ = st.Job(job.ID)
	if stored.Stats.Bytes != 500 || stored.Stats.TotalBytes != 1000 {
		t.Fatalf("cumulative stats = %+v, want 500/1000", stored.Stats)
	}

	// A later answer with a smaller remainder must not rewind the card.
	fake.responses["core/stats"] = map[string]any{"bytes": 20, "totalBytes": 100}
	manager.Poll(ctx)
	stored, _ = st.Job(job.ID)
	if stored.Stats.Bytes != 500 {
		t.Fatalf("bytes went backwards: %+v", stored.Stats)
	}
	if stored.Stats.TotalBytes != 1000 {
		t.Fatalf("total shrank: %+v", stored.Stats)
	}

	// The transfer may finish with fewer transferred bytes than planned: files
	// already in the destination are skipped. The stored numbers stay, the UI
	// renders 100 % of the full volume.
	fake.responses["job/status"] = map[string]any{"finished": true, "success": true}
	manager.Poll(ctx)
	stored, _ = st.Job(job.ID)
	if stored.Status != store.StatusDone {
		t.Fatalf("status = %q, want done", stored.Status)
	}
	if stored.Stats.Bytes != 500 || stored.Stats.TotalBytes != 1000 {
		t.Fatalf("final stats = %+v, want 500/1000", stored.Stats)
	}
}
