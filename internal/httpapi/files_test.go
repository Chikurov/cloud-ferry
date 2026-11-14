package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cloudferry/internal/jobs"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

// filesTestServer is a server with one saved account, so the handler can be
// exercised through the real HTTP stack.
func filesTestServer(t *testing.T) (*Server, *httptest.Server, *fakeEngine, string) {
	t.Helper()
	root := t.TempDir()
	api, server, engine := newRootedServer(t, []string{root}, root)
	return api, server, engine, createTestAccount(t, server)
}

// newFilesEngineServer builds a server around any Engine implementation, for
// tests that need to script a blocking or failing rclone.
func newFilesEngineServer(t *testing.T, engine Engine) (*Server, *httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	downloads := t.TempDir()
	api, err := New(Config{
		Store:     st,
		Rclone:    engine,
		Jobs:      jobs.NewManager(st, engine, jobs.Options{}),
		Downloads: downloads,
		DestRoots: []string{downloads},
		Version:   "test",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return api, server, st
}

func addFilesAccount(t *testing.T, st *store.Store, id, remote string) {
	t.Helper()
	account := store.Account{
		ID:        id,
		Name:      "Облако " + id,
		Provider:  "yandex-token",
		Remote:    remote,
		CreatedAt: time.Now().UTC(),
	}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
}

func setCloudList(engine *fakeEngine, items ...map[string]any) {
	engine.setResponse("operations/list", map[string]any{"list": items})
}

func getFiles(t *testing.T, server *httptest.Server, accountID, query string) (*http.Response, map[string]any) {
	t.Helper()
	target := "/api/accounts/" + url.PathEscape(accountID) + "/files"
	if query != "" {
		target += "?" + query
	}
	return getJSON(t, server, target)
}

func filesIn(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["files"].([]any)
	if !ok {
		t.Fatalf("files missing or null: %v", body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		file, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("file is not an object: %v", item)
		}
		out = append(out, file)
	}
	return out
}

func TestCloudFilesSuccess(t *testing.T) {
	_, server, engine, accountID := filesTestServer(t)
	setCloudList(engine,
		map[string]any{"Name": "Фото", "IsDir": true},
		map[string]any{"Name": "notes.txt", "IsDir": false, "Size": 30},
		map[string]any{"Name": "IMG_0002.jpg", "IsDir": false, "Size": 222, "ModTime": "2024-05-01T12:30:45.123456789+03:00"},
		map[string]any{"Name": "IMG_0001.jpg", "IsDir": false, "Size": 111, "ModTime": "2024-05-01T10:00:00Z"},
	)

	resp, body := getFiles(t, server, accountID, "path="+url.QueryEscape("Фотографии"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["path"] != "Фотографии" {
		t.Fatalf("path = %v", body["path"])
	}
	if body["truncated"] != false || body["count"] != float64(3) {
		t.Fatalf("truncated/count = %v/%v", body["truncated"], body["count"])
	}

	files := filesIn(t, body)
	wantNames := []string{"IMG_0001.jpg", "IMG_0002.jpg", "notes.txt"}
	for index, want := range wantNames {
		if files[index]["name"] != want {
			t.Fatalf("file %d = %v, want name %q", index, files[index], want)
		}
	}
	if files[0]["size"] != float64(111) || files[1]["size"] != float64(222) || files[2]["size"] != float64(30) {
		t.Fatalf("sizes = %v", files)
	}
	if files[0]["modified"] != "2024-05-01T10:00:00Z" {
		t.Fatalf("modified[0] = %v", files[0]["modified"])
	}
	// rclone reports the provider's offset; the answer is normalised to UTC.
	if files[1]["modified"] != "2024-05-01T09:30:45Z" {
		t.Fatalf("modified[1] = %v", files[1]["modified"])
	}
	if _, present := files[2]["modified"]; present {
		t.Fatalf("missing ModTime must omit modified: %v", files[2])
	}

	call, ok := engine.find("operations/list")
	if !ok {
		t.Fatal("operations/list was not called")
	}
	if call.input["fs"] != "cf_"+accountID+":" {
		t.Fatalf("fs = %v", call.input["fs"])
	}
	if call.input["remote"] != "Фотографии" {
		t.Fatalf("remote = %v", call.input["remote"])
	}
	opt, _ := call.input["opt"].(map[string]any)
	if opt["showHash"] != false {
		t.Fatalf("opt = %v, want showHash:false", call.input["opt"])
	}
	if _, ok := opt["dirsOnly"]; ok {
		t.Fatalf("files listing asked for dirsOnly: %v", call.input["opt"])
	}
}

func TestCloudFilesRootAndEmptyFolder(t *testing.T) {
	_, server, engine, accountID := filesTestServer(t)
	setCloudList(engine,
		map[string]any{"Name": "Фото", "IsDir": true},
		map[string]any{"Name": "Видео", "IsDir": true},
	)

	// No path means the cloud root.
	resp, body := getFiles(t, server, accountID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["path"] != "" {
		t.Fatalf("path = %v, want root", body["path"])
	}
	files := filesIn(t, body)
	if len(files) != 0 || body["count"] != float64(0) || body["truncated"] != false {
		t.Fatalf("empty root answer = %v", body)
	}
	call, _ := engine.find("operations/list")
	if call.input["remote"] != "" {
		t.Fatalf("root remote = %v, want empty", call.input["remote"])
	}

	// A named folder with no files still answers an empty array, not null.
	resp, body = getFiles(t, server, accountID, "path="+url.QueryEscape("Фото"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["path"] != "Фото" {
		t.Fatalf("path = %v", body["path"])
	}
	if files := filesIn(t, body); len(files) != 0 {
		t.Fatalf("files = %v", files)
	}
}

func TestCloudFilesLimitAndTruncated(t *testing.T) {
	_, server, engine, accountID := filesTestServer(t)

	items := make([]map[string]any, 0, 61)
	for i := 60; i >= 0; i-- {
		items = append(items, map[string]any{"Name": fmt.Sprintf("file-%03d.txt", i), "IsDir": false, "Size": int64(i)})
	}
	setCloudList(engine, items...)

	// Default page size is 50 and the folder has more files than that.
	_, body := getFiles(t, server, accountID, "path="+url.QueryEscape("Документы"))
	if body["truncated"] != true || body["count"] != float64(50) {
		t.Fatalf("truncated/count = %v/%v", body["truncated"], body["count"])
	}
	files := filesIn(t, body)
	if len(files) != 50 {
		t.Fatalf("files = %d, want 50", len(files))
	}
	if files[0]["name"] != "file-000.txt" || files[49]["name"] != "file-049.txt" {
		t.Fatalf("first/last = %v/%v", files[0]["name"], files[49]["name"])
	}

	// A limit above the file count returns everything, untruncated.
	_, body = getFiles(t, server, accountID, "path="+url.QueryEscape("Документы")+"&limit=100")
	if body["truncated"] != false || body["count"] != float64(61) {
		t.Fatalf("truncated/count = %v/%v", body["truncated"], body["count"])
	}

	// An explicit small limit cuts the sorted list.
	_, body = getFiles(t, server, accountID, "path="+url.QueryEscape("Документы")+"&limit=2")
	if body["truncated"] != true || body["count"] != float64(2) {
		t.Fatalf("truncated/count = %v/%v", body["truncated"], body["count"])
	}
	if files := filesIn(t, body); files[0]["name"] != "file-000.txt" || files[1]["name"] != "file-001.txt" {
		t.Fatalf("limited files = %v", files)
	}
}

func TestCloudFilesLimitAboveMaximumIsClamped(t *testing.T) {
	_, server, engine, accountID := filesTestServer(t)

	items := make([]map[string]any, 0, 201)
	for i := 200; i >= 0; i-- {
		items = append(items, map[string]any{"Name": fmt.Sprintf("f-%03d", i), "IsDir": false, "Size": 1})
	}
	setCloudList(engine, items...)

	resp, body := getFiles(t, server, accountID, "path="+url.QueryEscape("Большая")+"&limit=500")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["count"] != float64(200) || body["truncated"] != true {
		t.Fatalf("truncated/count = %v/%v", body["truncated"], body["count"])
	}
	if files := filesIn(t, body); len(files) != 200 {
		t.Fatalf("files = %d, want 200", len(files))
	}

	// The clamp happens before the cache key: 500, 300 and 200 are one page.
	for _, limit := range []string{"200", "300"} {
		if _, body := getFiles(t, server, accountID, "path="+url.QueryEscape("Большая")+"&limit="+limit); body["count"] != float64(200) {
			t.Fatalf("limit=%s answer = %v", limit, body)
		}
	}
	if got := engine.count("operations/list"); got != 1 {
		t.Fatalf("operations/list calls = %d, want 1 (clamped limits share the cache)", got)
	}
}

func TestCloudFilesRejectsInvalidLimit(t *testing.T) {
	_, server, engine, accountID := filesTestServer(t)
	setCloudList(engine, map[string]any{"Name": "a.txt", "IsDir": false, "Size": 1})

	for _, limit := range []string{"0", "-1", "abc", "1.5"} {
		resp, body := getFiles(t, server, accountID, "limit="+url.QueryEscape(limit))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("limit=%q status = %d, body = %v", limit, resp.StatusCode, body)
		}
		if message, _ := body["error"].(string); message == "" {
			t.Fatalf("limit=%q has no error message: %v", limit, body)
		}
	}
	if got := engine.count("operations/list"); got != 0 {
		t.Fatalf("invalid limit reached rclone: %d calls", got)
	}
}

func TestCloudFilesRejectsInvalidPath(t *testing.T) {
	_, server, engine, accountID := filesTestServer(t)
	setCloudList(engine, map[string]any{"Name": "a.txt", "IsDir": false, "Size": 1})

	for _, badPath := range []string{"../secret", "a/../../b", ".", "a\x01b"} {
		resp, body := getFiles(t, server, accountID, "path="+url.QueryEscape(badPath))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("path=%q status = %d, body = %v", badPath, resp.StatusCode, body)
		}
		if message, _ := body["error"].(string); message == "" {
			t.Fatalf("path=%q has no error message: %v", badPath, body)
		}
	}
	if got := engine.count("operations/list"); got != 0 {
		t.Fatalf("invalid path reached rclone: %d calls", got)
	}
}

func TestCloudFilesFolderNotFound(t *testing.T) {
	_, server, engine, accountID := filesTestServer(t)

	cases := []error{
		&rc.APIError{Method: "operations/list", Status: http.StatusNotFound, Body: "directory not found"},
		&rc.APIError{Method: "operations/list", Status: http.StatusInternalServerError, Body: "error listing: directory not found"},
	}
	for _, upstream := range cases {
		engine.setError("operations/list", upstream)
		resp, body := getFiles(t, server, accountID, "path="+url.QueryEscape("Пропавшая"))
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("upstream %v: status = %d, body = %v", upstream, resp.StatusCode, body)
		}
		message, _ := body["error"].(string)
		if !strings.Contains(message, "не найдена") {
			t.Fatalf("message = %q", message)
		}
		if strings.Contains(strings.ToLower(message), "rclone") ||
			strings.Contains(strings.ToLower(message), "directory") {
			t.Fatalf("message leaks upstream details: %q", message)
		}
	}

	// A failed listing is not cached: the next request retries upstream.
	engine.setError("operations/list", nil)
	setCloudList(engine, map[string]any{"Name": "a.txt", "IsDir": false, "Size": 1})
	if resp, body := getFiles(t, server, accountID, "path="+url.QueryEscape("Пропавшая")); resp.StatusCode != http.StatusOK {
		t.Fatalf("recovered status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestCloudFilesUnknownAccount(t *testing.T) {
	_, server, engine, _ := filesTestServer(t)
	resp, body := getFiles(t, server, "unknown", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); message == "" {
		t.Fatalf("no error message in %v", body)
	}
	if got := engine.count("operations/list"); got != 0 {
		t.Fatalf("unknown account reached rclone: %d calls", got)
	}
}

func TestCloudFilesCache(t *testing.T) {
	api, server, engine, accountID := filesTestServer(t)
	clock := newFakeClock()
	api.files.now = clock.Now
	setCloudList(engine,
		map[string]any{"Name": "a.txt", "IsDir": false, "Size": 1},
		map[string]any{"Name": "b.txt", "IsDir": false, "Size": 2},
	)

	query := "path=" + url.QueryEscape("Фото")
	if _, body := getFiles(t, server, accountID, query); body["path"] != "Фото" {
		t.Fatalf("first listing = %v", body)
	}
	if got := engine.count("operations/list"); got != 1 {
		t.Fatalf("operations/list calls = %d, want 1", got)
	}
	if _, body := getFiles(t, server, accountID, query); body["path"] != "Фото" {
		t.Fatalf("cached listing = %v", body)
	}
	if got := engine.count("operations/list"); got != 1 {
		t.Fatalf("cached request hit upstream: %d calls", got)
	}

	// The limit is part of the cache key: another page size is another entry.
	if _, body := getFiles(t, server, accountID, query+"&limit=1"); body["count"] != float64(1) {
		t.Fatalf("limited listing = %v", body)
	}
	if got := engine.count("operations/list"); got != 2 {
		t.Fatalf("operations/list calls = %d, want 2 for a new limit", got)
	}
	getFiles(t, server, accountID, query+"&limit=1")
	if got := engine.count("operations/list"); got != 2 {
		t.Fatalf("limited listing was not cached: %d calls", got)
	}

	clock.Advance(cloudFilesTTL + time.Second)
	if _, body := getFiles(t, server, accountID, query); body["path"] != "Фото" {
		t.Fatalf("refreshed listing = %v", body)
	}
	if got := engine.count("operations/list"); got != 3 {
		t.Fatalf("operations/list calls = %d, want 3 after TTL", got)
	}
}

// blockingListEngine never answers operations/list until its context ends, so
// the handler's own deadline is what stops the request.
type blockingListEngine struct{}

func (e *blockingListEngine) Call(ctx context.Context, method string, in, out any) error {
	<-ctx.Done()
	return ctx.Err()
}

func (e *blockingListEngine) Ping(context.Context) (rc.Version, error) {
	return rc.Version{Version: "test"}, nil
}

func TestCloudFilesTimeout(t *testing.T) {
	previous := cloudFilesTimeout
	cloudFilesTimeout = 150 * time.Millisecond
	t.Cleanup(func() { cloudFilesTimeout = previous })

	_, server, st := newFilesEngineServer(t, &blockingListEngine{})
	addFilesAccount(t, st, "acc1", "cf_acc1")

	start := time.Now()
	resp, body := getFiles(t, server, "acc1", "path="+url.QueryEscape("Фото"))
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("listing ignored its timeout: %v", elapsed)
	}
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	message, _ := body["error"].(string)
	if message == "" {
		t.Fatalf("no error message in %v", body)
	}
	if strings.Contains(strings.ToLower(message), "deadline") || strings.Contains(strings.ToLower(message), "rclone") {
		t.Fatalf("message leaks internals: %q", message)
	}
}

func TestCloudFilesRcloneUnavailable(t *testing.T) {
	_, server, engine, accountID := filesTestServer(t)

	cases := []error{
		errors.New("dial unix /run/rclone/rclone.sock: connect: connection refused"),
		&rc.APIError{Method: "operations/list", Status: http.StatusInternalServerError, Body: "failed to list: 401 unauthorized"},
	}
	for _, upstream := range cases {
		engine.setError("operations/list", upstream)
		resp, body := getFiles(t, server, accountID, "path="+url.QueryEscape("Фото"))
		if resp.StatusCode != http.StatusBadGateway {
			t.Fatalf("upstream %v: status = %d, body = %v", upstream, resp.StatusCode, body)
		}
		message, _ := body["error"].(string)
		if message == "" {
			t.Fatalf("no error message in %v", body)
		}
		lower := strings.ToLower(message)
		if strings.Contains(lower, "rclone") || strings.Contains(lower, "unauthorized") ||
			strings.Contains(lower, "connection refused") {
			t.Fatalf("message leaks upstream details: %q", message)
		}
	}
}
