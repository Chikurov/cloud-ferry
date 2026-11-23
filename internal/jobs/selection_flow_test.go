package jobs

import (
	"context"
	"errors"
	"slices"
	"testing"

	"cloudferry/internal/store"
)

func TestChangeDestinationPreservesExclusionsAndResetsProgress(t *testing.T) {
	manager, engine, st := newTestManager(t)
	first, second := t.TempDir(), t.TempDir()
	paused := store.Job{
		ID: "task", AccountID: "account", IdempotencyKey: "first-request", Source: "cf_account:", Mode: "copy",
		Dest: first, Status: store.StatusCanceled, Group: "old", ExcludedFolders: []string{"Архив"},
		Stats: store.Stats{Bytes: 80, TotalBytes: 100},
	}
	if err := st.AddJob(paused); err != nil {
		t.Fatal(err)
	}
	job, err := manager.ResumeTo(context.Background(), paused.ID, second)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != paused.ID || job.Dest != second || job.InitialDest != first || job.IdempotencyKey != "first-request" || job.BytesBase != 0 || job.Stats.Bytes != 0 {
		t.Fatalf("unexpected resumed job: %+v", job)
	}
	call := engine.inputFor(t, "sync/copy")
	filter, _ := call["_filter"].(map[string]any)
	rules, _ := filter["ExcludeRule"].([]string)
	if call["dstFs"] != second || !slices.Equal(rules, []string{"/Архив/**"}) {
		t.Fatalf("wrong transfer arguments: %v", call)
	}
	job.ExcludedFolders[0] = "other"
	stored, _ := st.Job(job.ID)
	if stored.ExcludedFolders[0] != "Архив" {
		t.Fatal("the returned exclusions mutated the stored selection")
	}
	manager.markFailed(job.ID, "old", errors.New("expired attempt"))
	stored, _ = st.Job(job.ID)
	if stored.Status != store.StatusRunning {
		t.Fatal("a late failure overwrote the new attempt")
	}
}

func TestChangedExclusionIsADifferentSource(t *testing.T) {
	if sameSource(Source{Excluded: []string{"Архив"}}, Source{}) {
		t.Fatal("different requests cannot replay the same download")
	}
}
