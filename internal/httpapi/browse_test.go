package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"cloudferry/internal/jobs"
	"cloudferry/internal/store"
)

func newRootedServer(t *testing.T, roots []string, downloads string) (*Server, *httptest.Server, *fakeEngine) {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &fakeEngine{errors: map[string]error{}, responses: map[string]any{
		"sync/copy":        map[string]any{"jobid": 11},
		"sync/sync":        map[string]any{"jobid": 12},
		"operations/about": map[string]any{"total": 1000, "used": 400, "free": 600},
		"operations/list":  map[string]any{"list": []map[string]any{{"Name": "Фото", "IsDir": true}, {"Name": "Файл.txt", "IsDir": false}}},
		"operations/size":  map[string]any{"count": 10, "bytes": 1000},
		"operations/stat":  map[string]any{"item": map[string]any{"IsDir": true}},
	}}
	manager := jobs.NewManager(st, engine, jobs.Options{})
	api, err := New(Config{
		Store:     st,
		Rclone:    engine,
		Jobs:      manager,
		Downloads: downloads,
		DestRoots: roots,
		Version:   "test",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return api, server, engine
}

func createTestAccount(t *testing.T, server *httptest.Server) string {
	t.Helper()
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create account: %d %v", resp.StatusCode, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("no account id in %v", body)
	}
	return id
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
}

func getJSON(t *testing.T, server *httptest.Server, path string) (*http.Response, map[string]any) {
	t.Helper()
	return doJSON(t, server.Client(), http.MethodGet, server.URL+path, nil)
}

func TestFSDirsReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	mkdirAll(t, locked)
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	_, server, _ := newRootedServer(t, []string{root}, root)
	resp, body := getJSON(t, server, "/api/fs/dirs?path="+url.QueryEscape(locked))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["writable"] != false {
		t.Fatalf("writable = %v", body["writable"])
	}
}

func TestFSDirsAndFoldersAliasShareResponse(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, "Фото"))
	mkdirAll(t, filepath.Join(root, "Видео"))
	writeTestFile(t, filepath.Join(root, "note.txt"))

	_, server, _ := newRootedServer(t, []string{root}, root)

	respDirs, bodyDirs := getJSON(t, server, "/api/fs/dirs?path="+url.QueryEscape(root))
	if respDirs.StatusCode != http.StatusOK {
		t.Fatalf("dirs status = %d, body = %v", respDirs.StatusCode, bodyDirs)
	}
	if got := respDirs.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	respFolders, bodyFolders := getJSON(t, server, "/api/fs/folders?path="+url.QueryEscape(root))
	if respFolders.StatusCode != http.StatusOK {
		t.Fatalf("folders status = %d, body = %v", respFolders.StatusCode, bodyFolders)
	}

	// Volatile values may differ between two calls; everything else must match.
	for _, body := range []map[string]any{bodyDirs, bodyFolders} {
		delete(body, "free")
		delete(body, "total")
	}
	if !reflect.DeepEqual(bodyDirs, bodyFolders) {
		t.Fatalf("aliases answered differently:\ndirs = %v\nfolders = %v", bodyDirs, bodyFolders)
	}

	if bodyDirs["root"] != root || bodyDirs["path"] != root {
		t.Fatalf("root/path = %v/%v", bodyDirs["root"], bodyDirs["path"])
	}
	if bodyDirs["parent"] != nil {
		t.Fatalf("parent = %v", bodyDirs["parent"])
	}
	if bodyDirs["truncated"] != false || bodyDirs["writable"] != true {
		t.Fatalf("flags = %v", bodyDirs)
	}
	dirs, _ := bodyDirs["dirs"].([]any)
	entries, _ := bodyDirs["entries"].([]any)
	if len(dirs) != 2 || len(entries) != 2 {
		t.Fatalf("dirs/entries = %v/%v", dirs, entries)
	}
	first, _ := dirs[0].(map[string]any)
	if first["name"] != "Видео" || first["path"] != filepath.Join(root, "Видео") {
		t.Fatalf("first dir = %v", first)
	}
	if !reflect.DeepEqual(dirs, entries) {
		t.Fatalf("entries alias mismatch: %v vs %v", dirs, entries)
	}
}

func TestFSDirsDefaultsToDownloads(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, "one"))
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := getJSON(t, server, "/api/fs/dirs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["path"] != root {
		t.Fatalf("path = %v, want %v", body["path"], root)
	}
}

func TestFSDirsErrors(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, "real"))
	writeTestFile(t, filepath.Join(root, "note.txt"))
	outside := t.TempDir()
	symlink(t, outside, filepath.Join(root, "escape"))
	_, server, _ := newRootedServer(t, []string{root}, root)

	cases := []struct {
		name   string
		path   string
		status int
	}{
		{"relative", "relative/path", http.StatusBadRequest},
		{"outside", "/etc", http.StatusForbidden},
		{"traversal", root + "/../etc", http.StatusForbidden},
		{"missing", filepath.Join(root, "missing"), http.StatusNotFound},
		{"file", filepath.Join(root, "note.txt"), http.StatusBadRequest},
		{"symlink", filepath.Join(root, "escape"), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := getJSON(t, server, "/api/fs/dirs?path="+url.QueryEscape(tc.path))
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d, body = %v", resp.StatusCode, tc.status, body)
			}
			message, _ := body["error"].(string)
			if message == "" {
				t.Fatalf("expected a Russian error message, body = %v", body)
			}
		})
	}
}

func TestFSDirsUnmountedRootIs500(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted")
	_, server, _ := newRootedServer(t, []string{missing}, missing)

	resp, body := getJSON(t, server, "/api/fs/dirs?path="+url.QueryEscape(missing))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	message, _ := body["error"].(string)
	if message == "" {
		t.Fatalf("expected error message, body = %v", body)
	}
}

func TestFSDirsTruncated(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 505; i++ {
		mkdirAll(t, filepath.Join(root, fmt.Sprintf("dir-%03d", i)))
	}
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := getJSON(t, server, "/api/fs/dirs?path="+url.QueryEscape(root))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	dirs, _ := body["dirs"].([]any)
	if len(dirs) != 500 {
		t.Fatalf("got %d dirs, want 500", len(dirs))
	}
	if body["truncated"] != true {
		t.Fatalf("truncated = %v", body["truncated"])
	}
}

func TestCloudFolders(t *testing.T) {
	root := t.TempDir()
	_, server, engine := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	resp, body := getJSON(t, server, "/api/accounts/"+accountID+"/folders?path="+url.QueryEscape("Фото"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["path"] != "Фото" || body["parent"] != "" {
		t.Fatalf("path/parent = %v/%v", body["path"], body["parent"])
	}
	dirs, _ := body["dirs"].([]any)
	entries, _ := body["entries"].([]any)
	if len(dirs) != 1 || len(entries) != 1 {
		t.Fatalf("dirs = %v, entries = %v", dirs, entries)
	}
	first, _ := dirs[0].(map[string]any)
	if first["name"] != "Фото" || first["path"] != "Фото/Фото" {
		t.Fatalf("dir = %v", first)
	}

	call, ok := engine.find("operations/list")
	if !ok {
		t.Fatal("operations/list was not called")
	}
	if call.input["remote"] != "Фото" {
		t.Fatalf("remote = %v", call.input["remote"])
	}
	opt, _ := call.input["opt"].(map[string]any)
	if opt["dirsOnly"] != true {
		t.Fatalf("opt = %v", call.input["opt"])
	}
}

func TestCloudFoldersEmptyPathIsRemoteRoot(t *testing.T) {
	root := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	resp, body := getJSON(t, server, "/api/accounts/"+accountID+"/folders")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["path"] != "" || body["parent"] != nil {
		t.Fatalf("path/parent = %v/%v", body["path"], body["parent"])
	}
	dirs, _ := body["dirs"].([]any)
	first, _ := dirs[0].(map[string]any)
	if first["path"] != "Фото" {
		t.Fatalf("dir = %v", first)
	}
}

func TestCloudFoldersErrors(t *testing.T) {
	root := t.TempDir()
	_, server, engine := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	resp, body := getJSON(t, server, "/api/accounts/unknown/folders")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown account status = %d, body = %v", resp.StatusCode, body)
	}
	resp, body = getJSON(t, server, "/api/accounts/"+accountID+"/folders?path="+url.QueryEscape("../secret"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal status = %d, body = %v", resp.StatusCode, body)
	}

	engine.setError("operations/list", errors.New("rclone down"))
	resp, body = getJSON(t, server, "/api/accounts/"+accountID+"/folders?path="+url.QueryEscape("Фото"))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("upstream error status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestCloudFoldersCache(t *testing.T) {
	root := t.TempDir()
	api, server, engine := newRootedServer(t, []string{root}, root)
	clock := newFakeClock()
	api.folders.now = clock.Now
	accountID := createTestAccount(t, server)

	if _, body := getJSON(t, server, "/api/accounts/"+accountID+"/folders?path="+url.QueryEscape("Фото")); body["path"] != "Фото" {
		t.Fatalf("first listing = %v", body)
	}
	if got := engine.count("operations/list"); got != 1 {
		t.Fatalf("operations/list calls = %d, want 1", got)
	}
	if _, body := getJSON(t, server, "/api/accounts/"+accountID+"/folders?path="+url.QueryEscape("Фото")); body["path"] != "Фото" {
		t.Fatalf("cached listing = %v", body)
	}
	if got := engine.count("operations/list"); got != 1 {
		t.Fatalf("cached call hit upstream: %d calls", got)
	}

	clock.Advance(61 * time.Second)
	if _, body := getJSON(t, server, "/api/accounts/"+accountID+"/folders?path="+url.QueryEscape("Фото")); body["path"] != "Фото" {
		t.Fatalf("refreshed listing = %v", body)
	}
	if got := engine.count("operations/list"); got != 2 {
		t.Fatalf("operations/list calls = %d, want 2 after TTL", got)
	}
}

func TestCloudFoldersLimit(t *testing.T) {
	root := t.TempDir()
	_, server, engine := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	list := make([]map[string]any, 0, 505)
	for i := 0; i < 505; i++ {
		list = append(list, map[string]any{"Name": fmt.Sprintf("dir-%03d", i), "IsDir": true})
	}
	engine.setResponse("operations/list", map[string]any{"list": list})

	resp, body := getJSON(t, server, "/api/accounts/"+accountID+"/folders")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	dirs, _ := body["dirs"].([]any)
	if len(dirs) != 500 || body["truncated"] != true {
		t.Fatalf("dirs=%d truncated=%v", len(dirs), body["truncated"])
	}
}

func TestBrowseAndJobValidationAgree(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, "real"))
	writeTestFile(t, filepath.Join(root, "note.txt"))
	outside := t.TempDir()
	symlink(t, outside, filepath.Join(root, "escape"))

	_, server, _ := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	// Browse shows only the real directory, never the symlink.
	_, listing := getJSON(t, server, "/api/fs/folders?path="+url.QueryEscape(root))
	dirs, _ := listing["dirs"].([]any)
	if len(dirs) != 1 {
		t.Fatalf("dirs = %v", dirs)
	}
	entry, _ := dirs[0].(map[string]any)
	if entry["name"] != "real" {
		t.Fatalf("entry = %v", entry)
	}

	// The directory the picker offered is accepted by job creation.
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": filepath.Join(root, "real"),
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("job into shown dir: %d %v", resp.StatusCode, body)
	}

	// Both preflight routes use the same validation as the picker.
	for _, route := range []string{"/api/preflight", "/api/jobs/preflight"} {
		resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+route, map[string]any{
			"accountId": accountID, "destPath": filepath.Join(root, "real", "new"),
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s valid dest: %d %v", route, resp.StatusCode, body)
		}
		resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+route, map[string]any{
			"accountId": accountID, "destPath": filepath.Join(root, "escape", "sub"),
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s symlink dest: %d %v", route, resp.StatusCode, body)
		}
	}

	// The picker cannot open the symlink either.
	resp, body = getJSON(t, server, "/api/fs/folders?path="+url.QueryEscape(filepath.Join(root, "escape")))
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("browse symlink: %d %v", resp.StatusCode, body)
	}

	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": filepath.Join(root, "escape", "sub"),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("job into symlink: %d %v", resp.StatusCode, body)
	}

	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": filepath.Join(root, "note.txt"),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("job into file: %d %v", resp.StatusCode, body)
	}
}

// fakeClock is a deterministic clock for cache and informer tests.
type fakeClock struct {
	mu sync.Mutex
	at time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{at: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.at
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.at = f.at.Add(d)
}
