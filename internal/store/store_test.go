package store

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAccountsAndJobsPersist(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	account := Account{ID: "abc", Name: "Яндекс", Provider: "webdav", Remote: "cf_abc", CreatedAt: time.Now().UTC()}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	job := Job{
		ID: "job1", AccountID: "abc", Source: "cf_abc:/", Dest: "/downloads/yandex",
		Mode: "copy", Status: StatusRunning, CreatedAt: time.Now().UTC(),
	}
	if err := st.AddJob(job); err != nil {
		t.Fatalf("add job: %v", err)
	}

	if err := st.UpdateJob("job1", func(j *Job) bool { j.Status = StatusDone; return true }); err != nil {
		t.Fatalf("update: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.Job("job1")
	if !ok {
		t.Fatal("job not found after reopen")
	}
	if got.Status != StatusDone {
		t.Fatalf("status = %q", got.Status)
	}
	if _, ok := reopened.Account("abc"); !ok {
		t.Fatal("account not found after reopen")
	}
}

func TestUpdateUnknownJob(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.UpdateJob("nope", func(*Job) bool { return true }); err == nil {
		t.Fatal("expected error for unknown job")
	}
}

func TestDeleteAccount(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddAccount(Account{ID: "a", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add: %v", err)
	}
	removed, err := st.DeleteAccount("a")
	if err != nil || !removed {
		t.Fatalf("delete: %v %v", removed, err)
	}
	if _, ok := st.Account("a"); ok {
		t.Fatal("account still present")
	}
}

func TestOpenRecoversFromBackup(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddAccount(Account{ID: "a1", Name: "Яндекс", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := st.AddAccount(Account{ID: "a2", Name: "Google", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Corrupt the primary file, keep the backup intact.
	path := dir + "/state.json"
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("open corrupt: %v", err)
	}
	accounts := reopened.Accounts()
	if len(accounts) != 1 || accounts[0].ID != "a1" {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestSaveKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddAccount(Account{ID: "a1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := st.AddAccount(Account{ID: "a2", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := os.Stat(dir + "/state.json.bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestStateCapsJobHistory(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 150; i++ {
		job := Job{ID: fmt.Sprintf("j%03d", i), Status: StatusDone, CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}
		if err := st.AddJob(job); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	if got := len(st.Jobs()); got > maxJobs {
		t.Fatalf("jobs = %d, want <= %d", got, maxJobs)
	}
}

func TestJobSourcePathsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	job := Job{
		ID: "job-multi", AccountID: "abc", Source: "cf_abc:",
		SourcePaths: []string{"Документы", "Фотографии/2024"},
		Dest:        "/downloads/yandex", Mode: "copy", Status: StatusDone, CreatedAt: time.Now().UTC(),
	}
	if err := st.AddJob(job); err != nil {
		t.Fatalf("add job: %v", err)
	}

	raw, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(raw), `"sourcePaths"`) {
		t.Fatalf("state does not keep sourcePaths: %s", raw)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.Job("job-multi")
	if !ok {
		t.Fatal("job not found after reopen")
	}
	if !reflect.DeepEqual(got.SourcePaths, []string{"Документы", "Фотографии/2024"}) {
		t.Fatalf("sourcePaths = %#v", got.SourcePaths)
	}
	if got.Source != "cf_abc:" {
		t.Fatalf("source = %q", got.Source)
	}
}

func TestJobSourceFilesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	job := Job{
		ID: "job-files", AccountID: "abc", Source: "cf_abc:",
		SourcePaths: []string{"Документы"},
		SourceFiles: []string{"a.txt", "b/c.md"},
		Dest:        "/downloads/yandex", Mode: "copy", Status: StatusDone, CreatedAt: time.Now().UTC(),
	}
	if err := st.AddJob(job); err != nil {
		t.Fatalf("add job: %v", err)
	}

	raw, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(raw), `"sourceFiles"`) {
		t.Fatalf("state does not keep sourceFiles: %s", raw)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.Job("job-files")
	if !ok {
		t.Fatal("job not found after reopen")
	}
	if !reflect.DeepEqual(got.SourceFiles, []string{"a.txt", "b/c.md"}) {
		t.Fatalf("sourceFiles = %#v", got.SourceFiles)
	}
	if !reflect.DeepEqual(got.SourcePaths, []string{"Документы"}) {
		t.Fatalf("sourcePaths = %#v", got.SourcePaths)
	}
	if got.Source != "cf_abc:" {
		t.Fatalf("source = %q", got.Source)
	}
}

// TestLegacyStateWithoutSourceFiles makes sure a state.json written by a build
// that only knew folder selections still loads: sourceFiles reads as nil and
// the folder list is untouched.
func TestLegacyStateWithoutSourceFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{
  "accounts": [{"id": "abc", "name": "Яндекс", "provider": "webdav", "remote": "cf_abc", "createdAt": "2026-11-13T20:00:00Z"}],
  "jobs": [{
    "id": "folders-only",
    "accountId": "abc",
    "source": "cf_abc:",
    "sourcePaths": ["Фото", "Документы"],
    "dest": "/downloads/yandex",
    "mode": "copy",
    "status": "done",
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
	job, ok := st.Job("folders-only")
	if !ok {
		t.Fatal("legacy job not loaded")
	}
	if job.SourceFiles != nil {
		t.Fatalf("sourceFiles = %#v, want nil", job.SourceFiles)
	}
	if !reflect.DeepEqual(job.SourcePaths, []string{"Фото", "Документы"}) {
		t.Fatalf("sourcePaths = %#v", job.SourcePaths)
	}
}

func TestJobWithoutSourcePathsOmitsField(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddJob(Job{
		ID: "legacy", Source: "cf_abc:/фото", Dest: "/downloads/yandex",
		Status: StatusDone, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	raw, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(raw), `"sourcePaths"`) {
		t.Fatalf("empty sourcePaths must be omitted: %s", raw)
	}
	if strings.Contains(string(raw), `"sourceFiles"`) {
		t.Fatalf("empty sourceFiles must be omitted: %s", raw)
	}
}

// TestLegacyStateWithoutSourcePaths makes sure a state.json written by an older
// build still loads: the missing field reads as nil and nothing else changes.
func TestLegacyStateWithoutSourcePaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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
	if job.SourcePaths != nil {
		t.Fatalf("sourcePaths = %#v, want nil", job.SourcePaths)
	}
	if job.Source != "cf_abc:/фото" || job.Dest != "/downloads/yandex" || job.Status != StatusDone {
		t.Fatalf("job = %+v", job)
	}
	if job.Stats.Bytes != 10 || job.RcloneJobID != 5 {
		t.Fatalf("legacy fields lost: %+v", job)
	}
	if accounts := st.Accounts(); len(accounts) != 1 || accounts[0].Remote != "cf_abc" {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestAccountLoginPersists(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	account := Account{
		ID: "abc", Name: "user@yandex.ru", Login: "user@yandex.ru",
		Provider: "webdav", Remote: "cf_abc", CreatedAt: time.Now().UTC(),
	}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.Account("abc")
	if !ok || got.Login != "user@yandex.ru" {
		t.Fatalf("login after reload = %q, ok = %v", got.Login, ok)
	}
	raw, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(raw), `"login": "user@yandex.ru"`) {
		t.Fatalf("login is not persisted: %s", raw)
	}
}

// TestLegacyStateWithoutAccountLogin makes sure state.json written before the
// login field existed still loads and is rewritten without inventing an empty
// key for older accounts.
func TestLegacyStateWithoutAccountLogin(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{
  "accounts": [{"id": "abc", "name": "Яндекс Диск", "provider": "webdav", "remote": "cf_abc", "createdAt": "2026-11-13T20:00:00Z"}],
  "jobs": []
}`
	if err := os.WriteFile(dir+"/state.json", []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	account, ok := st.Account("abc")
	if !ok || account.Name != "Яндекс Диск" || account.Login != "" {
		t.Fatalf("legacy account = %+v, ok = %v", account, ok)
	}

	if err := st.UpdateAccount("abc", func(a *Account) bool {
		a.Status = AccountStatusOK
		return true
	}); err != nil {
		t.Fatalf("update account: %v", err)
	}
	raw, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(raw), `"login"`) {
		t.Fatalf("empty login must stay omitted: %s", raw)
	}
}

// TestFailedSaveLeavesMemoryUnchanged makes the data directory read-only and
// checks that a failed save does not leave a half-applied change in memory.
func TestFailedSaveLeavesMemoryUnchanged(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks are not enforced for root")
	}
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddAccount(Account{ID: "a1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add a1: %v", err)
	}
	if err := st.AddAccount(Account{ID: "a2", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add a2: %v", err)
	}
	if err := st.AddJob(Job{ID: "j1", Status: StatusDone, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := st.AddAccount(Account{ID: "a3", CreatedAt: time.Now()}); err == nil {
		t.Fatal("AddAccount must fail without write permission")
	}
	if accounts := st.Accounts(); len(accounts) != 2 {
		t.Fatalf("accounts after failed add = %+v", accounts)
	}
	if err := st.UpdateJob("j1", func(j *Job) bool { j.Status = StatusRunning; return true }); err == nil {
		t.Fatal("UpdateJob must fail without write permission")
	}
	if job, _ := st.Job("j1"); job.Status != StatusDone {
		t.Fatalf("job after failed update = %+v", job)
	}
	removed, err := st.DeleteAccount("a1")
	if err == nil {
		t.Fatal("DeleteAccount must fail without write permission")
	}
	if !removed {
		t.Fatal("DeleteAccount should still report that the account exists")
	}
	if _, ok := st.Account("a1"); !ok {
		t.Fatal("account disappeared after a failed delete")
	}
}

func TestSnapshotDoesNotAliasStoredState(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddJob(Job{
		ID: "j1", Status: StatusDone, CreatedAt: time.Now(),
		SourcePaths: []string{"A"}, SourceFiles: []string{"a.txt"}, Failures: []string{"a.jpg"},
		Check: &CheckResult{Differ: 1},
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	_, jobs := st.Snapshot()
	jobs[0].SourcePaths[0] = "hacked"
	jobs[0].SourceFiles[0] = "hacked"
	jobs[0].Failures[0] = "hacked"
	jobs[0].Check.Differ = 99
	jobs[0].Status = StatusRunning

	stored, _ := st.Job("j1")
	if stored.SourcePaths[0] != "A" || stored.SourceFiles[0] != "a.txt" || stored.Failures[0] != "a.jpg" {
		t.Fatalf("snapshot aliases stored slices: %+v", stored)
	}
	if stored.Check.Differ != 1 || stored.Check == jobs[0].Check {
		t.Fatalf("snapshot aliases stored check: %+v", stored.Check)
	}
	if stored.Status != StatusDone {
		t.Fatalf("status = %q", stored.Status)
	}

	// UpdateJob hands the callback a copy as well: a rejected change must not
	// leak into the live state.
	if err := st.UpdateJob("j1", func(j *Job) bool {
		j.SourcePaths[0] = "changed"
		j.SourceFiles[0] = "changed"
		return false
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if stored, _ := st.Job("j1"); stored.SourcePaths[0] != "A" || stored.SourceFiles[0] != "a.txt" {
		t.Fatalf("rejected update leaked: %+v %+v", stored.SourcePaths, stored.SourceFiles)
	}
}

func TestRecoveryFromBackupResavesAndKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.AddAccount(Account{ID: "a1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add a1: %v", err)
	}
	if err := st.AddAccount(Account{ID: "a2", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add a2: %v", err)
	}
	// state.json now has a1+a2, .bak has a1.
	if err := os.WriteFile(dir+"/state.json", []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("open corrupt: %v", err)
	}
	if accounts := reopened.Accounts(); len(accounts) != 1 || accounts[0].ID != "a1" {
		t.Fatalf("recovered accounts = %+v", accounts)
	}

	// The recovered state is written back at once.
	raw, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("state.json was not repaired: %s", raw)
	}

	// The unreadable state.json must never overwrite the good backup.
	backup, err := os.ReadFile(dir + "/state.json.bak")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !json.Valid(backup) || strings.Contains(string(backup), "not json") {
		t.Fatalf("backup was overwritten with the corrupt state: %s", backup)
	}

	// A later save refreshes the backup with the repaired state.
	if err := reopened.AddAccount(Account{ID: "a3", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add a3: %v", err)
	}
	raw, _ = os.ReadFile(dir + "/state.json")
	backup, _ = os.ReadFile(dir + "/state.json.bak")
	if !json.Valid(raw) || !json.Valid(backup) {
		t.Fatalf("state or backup is invalid after the next save")
	}
	if !strings.Contains(string(backup), `"id": "a1"`) || strings.Contains(string(backup), `"id": "a3"`) {
		t.Fatalf("backup does not hold the previous state: %s", backup)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if accounts := again.Accounts(); len(accounts) != 2 {
		t.Fatalf("accounts after reopen = %+v", accounts)
	}
}
