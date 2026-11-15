package store

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestJobIdempotencyKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddJob(Job{
		ID: "idem", AccountID: "abc", IdempotencyKey: "key-1234",
		Source: "cf_abc:/фото", Dest: "/downloads/yandex",
		Status: StatusDone, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	raw, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(raw), `"idempotencyKey": "key-1234"`) {
		t.Fatalf("idempotency key is not stored: %s", raw)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	job, ok := reopened.Job("idem")
	if !ok {
		t.Fatal("job not loaded")
	}
	if job.IdempotencyKey != "key-1234" {
		t.Fatalf("idempotencyKey = %q, want key-1234", job.IdempotencyKey)
	}
}

// TestJobWithoutIdempotencyKeyOmitsField keeps the state file unchanged for
// requests that carry no key.
func TestJobWithoutIdempotencyKeyOmitsField(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddJob(Job{
		ID: "plain", Source: "cf_abc:/фото", Dest: "/downloads/yandex",
		Status: StatusDone, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	raw, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(raw), `"idempotencyKey"`) {
		t.Fatalf("empty idempotencyKey must be omitted: %s", raw)
	}
}

// TestLegacyStateWithoutIdempotencyKey makes sure a state.json written before
// the field existed still loads: the key reads as empty and nothing else moves.
func TestLegacyStateWithoutIdempotencyKey(t *testing.T) {
	dir := t.TempDir()
	legacy := `{
  "accounts": [{"id": "abc", "name": "Яндекс", "provider": "webdav", "remote": "cf_abc", "createdAt": "2026-11-13T20:00:00Z"}],
  "jobs": [{
    "id": "old-job",
    "accountId": "abc",
    "source": "cf_abc:/фото",
    "dest": "/downloads/yandex",
    "mode": "copy",
    "status": "done",
    "group": "migrate/old-job/1",
    "rcloneJobId": 5,
    "stats": {"bytes": 10, "totalBytes": 10},
    "createdAt": "2026-11-13T20:01:00Z"
  }]
}`
	if err := os.WriteFile(dir+"/state.json", []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	job, ok := st.Job("old-job")
	if !ok {
		t.Fatal("legacy job not loaded")
	}
	if job.IdempotencyKey != "" {
		t.Fatalf("idempotencyKey = %q, want empty", job.IdempotencyKey)
	}
	if job.Source != "cf_abc:/фото" || job.Dest != "/downloads/yandex" || job.Status != StatusDone {
		t.Fatalf("legacy fields lost: %+v", job)
	}
	if job.Stats.Bytes != 10 || job.RcloneJobID != 5 {
		t.Fatalf("legacy fields lost: %+v", job)
	}
}
