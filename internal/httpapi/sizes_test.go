package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"cloudferry/internal/jobs"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

// scriptedSizeEngine answers operations/size per srcFs and records what it was
// asked for. Real rclone is never involved.
type scriptedSizeEngine struct {
	mu    sync.Mutex
	calls []string
	size  func(ctx context.Context, fs string) (files, bytes int64, err error)
}

func (e *scriptedSizeEngine) Call(ctx context.Context, method string, in, out any) error {
	if method != "operations/size" {
		return fmt.Errorf("unexpected rc method in test: %s", method)
	}
	input, _ := in.(map[string]any)
	fs, _ := input["fs"].(string)
	e.mu.Lock()
	e.calls = append(e.calls, fs)
	e.mu.Unlock()

	files, bytes, err := e.size(ctx, fs)
	if err != nil {
		return err
	}
	payload, marshalErr := json.Marshal(map[string]int64{"count": files, "bytes": bytes})
	if marshalErr != nil {
		return marshalErr
	}
	return json.Unmarshal(payload, out)
}

func (e *scriptedSizeEngine) Ping(context.Context) (rc.Version, error) {
	return rc.Version{Version: "v1.76.0"}, nil
}

func (e *scriptedSizeEngine) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

// fsCalls returns every srcFs passed to operations/size, sorted so tests do not
// depend on the order of concurrent workers.
func (e *scriptedSizeEngine) fsCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := append([]string(nil), e.calls...)
	sort.Strings(out)
	return out
}

// newSizesTestServer builds a server around any Engine implementation, so the
// size tests can script a different answer per folder.
func newSizesTestServer(t *testing.T, engine Engine) (*Server, *httptest.Server, *store.Store) {
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

func addSizesAccount(t *testing.T, st *store.Store, id, remote string) store.Account {
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
	return account
}

func postSizes(t *testing.T, server *httptest.Server, payload any) (*http.Response, map[string]any) {
	t.Helper()
	return doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/cloud/sizes", payload)
}

func sizeEntries(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["entries"].([]any)
	if !ok {
		t.Fatalf("entries missing or mistyped: %v", body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("entry is not an object: %v", item)
		}
		out = append(out, entry)
	}
	return out
}

func TestCloudSizesSuccess(t *testing.T) {
	engine := &scriptedSizeEngine{size: func(_ context.Context, fs string) (int64, int64, error) {
		switch fs {
		case "cf_acc1:Фотографии":
			return 45, 123, nil
		case "cf_acc1:Документы/2024":
			return 7, 900, nil
		case "cf_acc1:Пустая":
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("unexpected fs %q", fs)
	}}
	_, server, st := newSizesTestServer(t, engine)
	addSizesAccount(t, st, "acc1", "cf_acc1")

	resp, body := postSizes(t, server, map[string]any{
		"accountId": " acc1 ",
		"paths":     []string{" /Фотографии ", "Документы/2024", "Пустая"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	entries := sizeEntries(t, body)
	if len(entries) != 3 {
		t.Fatalf("entries = %v", entries)
	}
	if entries[0]["path"] != "Фотографии" || entries[1]["path"] != "Документы/2024" || entries[2]["path"] != "Пустая" {
		t.Fatalf("order or normalisation is wrong: %v", entries)
	}
	if entries[0]["bytes"] != float64(123) || entries[0]["files"] != float64(45) {
		t.Fatalf("first entry = %v", entries[0])
	}
	if entries[1]["bytes"] != float64(900) || entries[1]["files"] != float64(7) {
		t.Fatalf("second entry = %v", entries[1])
	}
	// An empty folder reports real zeros, not a missing value: the UI must be
	// able to tell "0 файлов" from "не посчитали".
	if _, ok := entries[2]["bytes"]; !ok {
		t.Fatalf("empty folder has no bytes: %v", entries[2])
	}
	if entries[2]["bytes"] != float64(0) || entries[2]["files"] != float64(0) {
		t.Fatalf("empty folder = %v", entries[2])
	}

	wantCalls := []string{"cf_acc1:Документы/2024", "cf_acc1:Пустая", "cf_acc1:Фотографии"}
	if got := engine.fsCalls(); strings.Join(got, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("operations/size calls = %v, want %v", got, wantCalls)
	}
}

func TestCloudSizesPartialFailure(t *testing.T) {
	missing := &rc.APIError{Method: "operations/size", Status: http.StatusNotFound, Body: "directory not found"}
	engine := &scriptedSizeEngine{size: func(_ context.Context, fs string) (int64, int64, error) {
		if fs == "cf_acc1:Пропала" {
			return 0, 0, missing
		}
		return 2, 20, nil
	}}
	_, server, st := newSizesTestServer(t, engine)
	addSizesAccount(t, st, "acc1", "cf_acc1")

	resp, body := postSizes(t, server, map[string]any{
		"accountId": "acc1",
		"paths":     []string{"Фотографии", "Пропала", "Документы"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a failed folder must not fail the answer: %d %v", resp.StatusCode, body)
	}
	entries := sizeEntries(t, body)
	if len(entries) != 3 {
		t.Fatalf("entries = %v", entries)
	}
	if entries[1]["error"] != cloudSizeNotFoundMessage {
		t.Fatalf("missing folder entry = %v", entries[1])
	}
	if _, ok := entries[1]["bytes"]; ok {
		t.Fatalf("failed entry must not report bytes: %v", entries[1])
	}
	for _, index := range []int{0, 2} {
		if entries[index]["bytes"] != float64(20) || entries[index]["files"] != float64(2) {
			t.Fatalf("measured entry %d = %v", index, entries[index])
		}
		if _, ok := entries[index]["error"]; ok {
			t.Fatalf("successful entry carries an error: %v", entries[index])
		}
	}
}

func TestCloudSizesPathLimit(t *testing.T) {
	engine := &scriptedSizeEngine{size: func(context.Context, string) (int64, int64, error) {
		return 1, 10, nil
	}}
	_, server, st := newSizesTestServer(t, engine)
	addSizesAccount(t, st, "acc1", "cf_acc1")

	tooMany := make([]string, 0, maxCloudSizePaths+1)
	for i := 0; i < maxCloudSizePaths+1; i++ {
		tooMany = append(tooMany, fmt.Sprintf("Папка-%02d", i))
	}
	resp, body := postSizes(t, server, map[string]any{"accountId": "acc1", "paths": tooMany})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("21 paths: status = %d, body = %v", resp.StatusCode, body)
	}
	if body["field"] != "paths" {
		t.Fatalf("field = %v", body["field"])
	}
	if got := engine.callCount(); got != 0 {
		t.Fatalf("over-limit request reached rclone: %d calls", got)
	}

	justEnough := tooMany[:maxCloudSizePaths]
	resp, body = postSizes(t, server, map[string]any{"accountId": "acc1", "paths": justEnough})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("20 paths: status = %d, body = %v", resp.StatusCode, body)
	}
	if entries := sizeEntries(t, body); len(entries) != maxCloudSizePaths {
		t.Fatalf("entries = %d, want %d", len(entries), maxCloudSizePaths)
	}
	if got := engine.callCount(); got != maxCloudSizePaths {
		t.Fatalf("operations/size calls = %d, want %d", got, maxCloudSizePaths)
	}
}

func TestCloudSizesCache(t *testing.T) {
	engine := &scriptedSizeEngine{size: func(context.Context, string) (int64, int64, error) {
		return 3, 30, nil
	}}
	api, server, st := newSizesTestServer(t, engine)
	addSizesAccount(t, st, "acc1", "cf_acc1")
	clock := newFakeClock()
	api.sizes.now = clock.Now

	if resp, body := postSizes(t, server, map[string]any{"accountId": "acc1", "paths": []string{"Фотографии"}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("first call: %d %v", resp.StatusCode, body)
	}
	if got := engine.callCount(); got != 1 {
		t.Fatalf("operations/size calls = %d, want 1", got)
	}

	// The second call is served from memory, even when the path is spelled with
	// a surrounding slash.
	if resp, body := postSizes(t, server, map[string]any{"accountId": "acc1", "paths": []string{"/Фотографии/"}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("cached call: %d %v", resp.StatusCode, body)
	}
	if got := engine.callCount(); got != 1 {
		t.Fatalf("cached request hit upstream: %d calls", got)
	}

	// A different folder is a different key.
	if resp, body := postSizes(t, server, map[string]any{"accountId": "acc1", "paths": []string{"Документы"}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("other path: %d %v", resp.StatusCode, body)
	}
	if got := engine.callCount(); got != 2 {
		t.Fatalf("different path was served from cache: %d calls", got)
	}

	// After the TTL the folder is measured again.
	clock.Advance(cloudSizesTTL + time.Second)
	if resp, body := postSizes(t, server, map[string]any{"accountId": "acc1", "paths": []string{"Фотографии"}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("expired call: %d %v", resp.StatusCode, body)
	}
	if got := engine.callCount(); got != 3 {
		t.Fatalf("expired entry was not refreshed: %d calls", got)
	}
}

func TestCloudSizesCacheLimit(t *testing.T) {
	clock := newFakeClock()
	cache := newCloudSizesCache(clock.Now)

	for i := 0; i < maxCloudSizesCached+3; i++ {
		cache.put("acc1", fmt.Sprintf("Папка-%03d", i), cloudSize{Bytes: int64(i)})
		clock.Advance(time.Second)
	}
	if got := len(cache.items); got != maxCloudSizesCached {
		t.Fatalf("cache size = %d, want %d", got, maxCloudSizesCached)
	}
	if _, ok := cache.get("acc1", "Папка-000"); ok {
		t.Fatal("the oldest entry was not evicted")
	}
	if _, ok := cache.get("acc1", fmt.Sprintf("Папка-%03d", maxCloudSizesCached+2)); !ok {
		t.Fatal("the newest entry was evicted")
	}
}

func TestCloudSizesTimeoutMarksUnfinishedPaths(t *testing.T) {
	previous := cloudSizesTimeout
	cloudSizesTimeout = 250 * time.Millisecond
	t.Cleanup(func() { cloudSizesTimeout = previous })

	engine := &scriptedSizeEngine{size: func(ctx context.Context, fs string) (int64, int64, error) {
		if fs == "cf_acc1:Медленная" {
			<-ctx.Done()
			return 0, 0, ctx.Err()
		}
		return 2, 20, nil
	}}
	_, server, st := newSizesTestServer(t, engine)
	addSizesAccount(t, st, "acc1", "cf_acc1")

	start := time.Now()
	resp, body := postSizes(t, server, map[string]any{
		"accountId": "acc1",
		"paths":     []string{"Быстрая", "Медленная"},
	})
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("batch ignored its timeout: %v", elapsed)
	}
	entries := sizeEntries(t, body)
	if entries[0]["bytes"] != float64(20) {
		t.Fatalf("finished folder lost its size: %v", entries[0])
	}
	if entries[1]["error"] != cloudSizeTimeoutMessage {
		t.Fatalf("unfinished folder = %v", entries[1])
	}
}

func TestCloudSizesLimitsParallelism(t *testing.T) {
	const pathsCount = 8
	started := make(chan struct{}, pathsCount)
	release := make(chan struct{})

	var mu sync.Mutex
	active, maxActive := 0, 0
	engine := &scriptedSizeEngine{size: func(ctx context.Context, _ string) (int64, int64, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			mu.Lock()
			active--
			mu.Unlock()
			return 0, 0, ctx.Err()
		}
		mu.Lock()
		active--
		mu.Unlock()
		return 1, 10, nil
	}}
	_, server, st := newSizesTestServer(t, engine)
	addSizesAccount(t, st, "acc1", "cf_acc1")

	paths := make([]string, 0, pathsCount)
	for i := 0; i < pathsCount; i++ {
		paths = append(paths, fmt.Sprintf("Папка-%02d", i))
	}
	payload, err := json.Marshal(map[string]any{"accountId": "acc1", "paths": paths})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	statusCh := make(chan int, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, server.URL+"/api/cloud/sizes", strings.NewReader(string(payload)))
		if err != nil {
			statusCh <- -1
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := server.Client().Do(req)
		if err != nil {
			statusCh <- -1
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		statusCh <- resp.StatusCode
	}()

	// Exactly the configured number of calls may start before any is answered.
	for i := 0; i < cloudSizesParallelism; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d of %d size calls started", i, cloudSizesParallelism)
		}
	}
	select {
	case <-started:
		t.Fatalf("more than %d size calls ran at once", cloudSizesParallelism)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case status := <-statusCh:
		if status != http.StatusOK {
			t.Fatalf("status = %d, body not read", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}

	mu.Lock()
	peak := maxActive
	mu.Unlock()
	if peak != cloudSizesParallelism {
		t.Fatalf("peak concurrency = %d, want %d", peak, cloudSizesParallelism)
	}
	if got := engine.callCount(); got != pathsCount {
		t.Fatalf("operations/size calls = %d, want %d", got, pathsCount)
	}
}

func TestCloudSizesWholeRequestTimeout(t *testing.T) {
	engine := &scriptedSizeEngine{size: func(ctx context.Context, _ string) (int64, int64, error) {
		<-ctx.Done()
		return 0, 0, ctx.Err()
	}}
	api, _, st := newSizesTestServer(t, engine)
	addSizesAccount(t, st, "acc1", "cf_acc1")

	payload := strings.NewReader(`{"accountId":"acc1","paths":["Фотографии"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cloud/sizes", payload)
	req.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	recorder := httptest.NewRecorder()
	api.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode answer: %v", err)
	}
	if message, _ := body["error"].(string); message == "" {
		t.Fatalf("no error message in %v", body)
	}
}

func TestCloudSizesRcloneUnavailable(t *testing.T) {
	engine := &scriptedSizeEngine{size: func(context.Context, string) (int64, int64, error) {
		return 0, 0, errors.New("dial unix /run/rclone/rclone.sock: connect: connection refused")
	}}
	_, server, st := newSizesTestServer(t, engine)
	addSizesAccount(t, st, "acc1", "cf_acc1")

	resp, body := postSizes(t, server, map[string]any{"accountId": "acc1", "paths": []string{"Фотографии", "Документы"}})
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	message, _ := body["error"].(string)
	if message == "" {
		t.Fatalf("no error message in %v", body)
	}
	for _, leak := range []string{"dial", "unix", "rclone.sock", "connection refused"} {
		if strings.Contains(strings.ToLower(message), leak) {
			t.Fatalf("answer leaks rclone details: %q", message)
		}
	}
}

func TestCloudSizesInvalidRequests(t *testing.T) {
	engine := &scriptedSizeEngine{size: func(context.Context, string) (int64, int64, error) {
		return 0, 0, nil
	}}
	_, server, st := newSizesTestServer(t, engine)
	addSizesAccount(t, st, "acc1", "cf_acc1")

	tooMany := make([]string, 0, maxCloudSizePaths+1)
	for i := 0; i < maxCloudSizePaths+1; i++ {
		tooMany = append(tooMany, fmt.Sprintf("Папка-%02d", i))
	}
	tooManyPayload, err := json.Marshal(map[string]any{"accountId": "acc1", "paths": tooMany})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	cases := []struct {
		name    string
		payload string
		field   string
	}{
		{"malformed json", `{"accountId":`, ""},
		{"unknown field", `{"accountId":"acc1","paths":["Фото"],"extra":1}`, ""},
		{"no paths", `{"accountId":"acc1"}`, "paths"},
		{"empty paths", `{"accountId":"acc1","paths":[]}`, "paths"},
		{"null paths", `{"accountId":"acc1","paths":null}`, "paths"},
		{"traversal", `{"accountId":"acc1","paths":["../secret"]}`, "paths"},
		{"empty item", `{"accountId":"acc1","paths":[""]}`, "paths"},
		{"control character", `{"accountId":"acc1","paths":["Фото\u0001"]}`, "paths"},
		{"too many paths", string(tooManyPayload), "paths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, server.URL+"/api/cloud/sizes", strings.NewReader(tc.payload))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			var body map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
			}
			if message, _ := body["error"].(string); message == "" {
				t.Fatalf("no error message in %v", body)
			}
			if tc.field != "" && body["field"] != tc.field {
				t.Fatalf("field = %v, want %q", body["field"], tc.field)
			}
		})
	}
	if got := engine.callCount(); got != 0 {
		t.Fatalf("invalid requests reached rclone: %d calls", got)
	}
}

func TestCloudSizesUnknownAccount(t *testing.T) {
	engine := &scriptedSizeEngine{size: func(context.Context, string) (int64, int64, error) {
		return 0, 0, nil
	}}
	_, server, _ := newSizesTestServer(t, engine)

	resp, body := postSizes(t, server, map[string]any{"accountId": "unknown", "paths": []string{"Фотографии"}})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); message == "" {
		t.Fatalf("no error message in %v", body)
	}
	if got := engine.callCount(); got != 0 {
		t.Fatalf("unknown account reached rclone: %d calls", got)
	}
}
