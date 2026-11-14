package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"cloudferry/internal/jobs"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

func newAccountStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.AddAccount(store.Account{
		ID: "acc-1", Name: "Яндекс", Provider: "yandex-token",
		Remote: "cf_1", CreatedAt: time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("add account: %v", err)
	}
	return st
}

func TestCloudInformerStatuses(t *testing.T) {
	cases := []struct {
		name        string
		response    any
		err         error
		wantStatus  string
		wantNumbers bool
	}{
		{
			name:        "ok",
			response:    map[string]any{"total": 1000, "used": 400, "free": 600},
			wantStatus:  cloudStatusOK,
			wantNumbers: true,
		},
		{
			name:        "empty zeros",
			response:    map[string]any{"total": 0, "used": 0, "free": 0},
			wantStatus:  cloudStatusEmpty,
			wantNumbers: false,
		},
		{
			name:        "401",
			err:         &rc.APIError{Method: "operations/about", Status: http.StatusUnauthorized, Body: "unauthorized"},
			wantStatus:  cloudStatusNeedsAuth,
			wantNumbers: false,
		},
		{
			name:        "410",
			err:         &rc.APIError{Method: "operations/about", Status: http.StatusGone, Body: "gone"},
			wantStatus:  cloudStatusNeedsAuth,
			wantNumbers: false,
		},
		{
			name:        "410 in body with 200",
			err:         &rc.APIError{Method: "operations/about", Status: http.StatusOK, Body: "[410 - GoneError] Gone"},
			wantStatus:  cloudStatusNeedsAuth,
			wantNumbers: false,
		},
		{
			name:        "network failure",
			err:         errors.New("connect: network is unreachable"),
			wantStatus:  cloudStatusUnavailable,
			wantNumbers: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newAccountStore(t)
			engine := &fakeEngine{responses: map[string]any{}, errors: map[string]error{}}
			if tc.response != nil {
				engine.responses["operations/about"] = tc.response
			}
			if tc.err != nil {
				engine.errors["operations/about"] = tc.err
			}
			clock := newFakeClock()
			informer := newCloudInformer(st, engine, clock.Now)

			before := informer.snapshot()
			if before.Status != cloudStatusUnavailable || !before.Stale || before.Available {
				t.Fatalf("snapshot before refresh = %+v", before)
			}

			informer.refresh(context.Background())
			snapshot := informer.snapshot()
			if snapshot.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", snapshot.Status, tc.wantStatus)
			}
			if snapshot.Available != tc.wantNumbers {
				t.Fatalf("available = %v, want %v", snapshot.Available, tc.wantNumbers)
			}
			if snapshot.Stale {
				t.Fatal("fresh snapshot reported stale")
			}
			if tc.wantNumbers {
				if snapshot.Total == nil || *snapshot.Total != 1000 || snapshot.Used == nil || *snapshot.Used != 400 || snapshot.Free == nil || *snapshot.Free != 600 {
					t.Fatalf("numbers = %+v", snapshot)
				}
				if snapshot.UpdatedAt == nil || !snapshot.UpdatedAt.Equal(clock.Now()) {
					t.Fatalf("updatedAt = %v", snapshot.UpdatedAt)
				}
			} else if snapshot.Total != nil || snapshot.Used != nil || snapshot.Free != nil {
				t.Fatalf("unavailable data must be null, got %+v", snapshot)
			}

			clock.Advance(61 * time.Second)
			if stale := informer.snapshot(); !stale.Stale {
				t.Fatal("snapshot after TTL is not stale")
			}
		})
	}
}

func TestCloudInformerWithoutAccountsIsEmpty(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &fakeEngine{responses: map[string]any{}}
	informer := newCloudInformer(st, engine, time.Now)

	snapshot := informer.snapshot()
	if snapshot.Status != cloudStatusEmpty || snapshot.Available || !snapshot.Stale {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	informer.refresh(context.Background())
	if informer.snapshot().Status != cloudStatusEmpty {
		t.Fatalf("after refresh = %+v", informer.snapshot())
	}
	if engine.count("operations/about") != 0 {
		t.Fatal("informer called rclone without an account")
	}
}

func TestSystemRootsReportTotal(t *testing.T) {
	root := t.TempDir()
	mkdirAll(t, filepath.Join(root, "one"))
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := getJSON(t, server, "/api/system")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	roots, _ := body["roots"].([]any)
	if len(roots) != 1 {
		t.Fatalf("roots = %v", body["roots"])
	}
	info, _ := roots[0].(map[string]any)
	if info["path"] != root {
		t.Fatalf("root path = %v", info["path"])
	}
	free, _ := info["free"].(float64)
	total, _ := info["total"].(float64)
	if free <= 0 || total <= 0 || free > total {
		t.Fatalf("free/total = %v/%v", free, total)
	}
}

func TestSystemCloudEmptyWithoutAccounts(t *testing.T) {
	root := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := getJSON(t, server, "/api/system")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	cloud, _ := body["cloud"].(map[string]any)
	if cloud["status"] != cloudStatusEmpty || cloud["available"] != false {
		t.Fatalf("cloud = %v", cloud)
	}
}

func TestSystemCloudInformerEndToEnd(t *testing.T) {
	root := t.TempDir()
	api, server, engine := newRootedServer(t, []string{root}, root)
	clock := newFakeClock()
	api.cloud.now = clock.Now
	accountID := createTestAccount(t, server)

	// The first request answers immediately with no data and starts a refresh.
	_, body := getJSON(t, server, "/api/system")
	cloud, _ := body["cloud"].(map[string]any)
	if cloud["status"] != cloudStatusUnavailable || cloud["available"] != false || cloud["stale"] != true {
		t.Fatalf("first cloud = %v", cloud)
	}
	if cloud["total"] != nil {
		t.Fatalf("unavailable total = %v, want null", cloud["total"])
	}
	api.cloud.inflight.Wait()

	// The cache now serves the fetched numbers without another upstream call.
	_, body = getJSON(t, server, "/api/system")
	cloud, _ = body["cloud"].(map[string]any)
	if cloud["status"] != cloudStatusOK || cloud["available"] != true || cloud["stale"] != false {
		t.Fatalf("cached cloud = %v", cloud)
	}
	if cloud["total"] != float64(1000) || cloud["used"] != float64(400) || cloud["free"] != float64(600) {
		t.Fatalf("cloud numbers = %v", cloud)
	}
	if cloud["accountId"] != accountID || cloud["provider"] != "yandex-token" || cloud["name"] != "Яндекс" {
		t.Fatalf("cloud identity = %v", cloud)
	}
	if cloud["updatedAt"] == nil {
		t.Fatalf("updatedAt missing: %v", cloud)
	}
	if got := engine.count("operations/about"); got != 1 {
		t.Fatalf("operations/about calls = %d, want 1", got)
	}

	// After the TTL the old numbers are served with stale=true and refreshed.
	clock.Advance(61 * time.Second)
	_, body = getJSON(t, server, "/api/system")
	cloud, _ = body["cloud"].(map[string]any)
	if cloud["status"] != cloudStatusOK || cloud["stale"] != true {
		t.Fatalf("stale cloud = %v", cloud)
	}
	api.cloud.inflight.Wait()
	_, body = getJSON(t, server, "/api/system")
	cloud, _ = body["cloud"].(map[string]any)
	if cloud["stale"] != false {
		t.Fatalf("cloud after refresh = %v", cloud)
	}
	if got := engine.count("operations/about"); got != 2 {
		t.Fatalf("operations/about calls = %d, want 2", got)
	}

	// An auth failure becomes needs-auth and drops the numbers.
	engine.setError("operations/about", &rc.APIError{
		Method: "operations/about", Status: http.StatusGone, Body: "[410 - GoneError] Gone",
	})
	clock.Advance(61 * time.Second)
	getJSON(t, server, "/api/system")
	api.cloud.inflight.Wait()
	_, body = getJSON(t, server, "/api/system")
	cloud, _ = body["cloud"].(map[string]any)
	if cloud["status"] != cloudStatusNeedsAuth || cloud["available"] != false || cloud["total"] != nil {
		t.Fatalf("needs-auth cloud = %v", cloud)
	}

	// A transient failure is unavailable, not needs-auth.
	engine.setError("operations/about", errors.New("connect: network is unreachable"))
	clock.Advance(61 * time.Second)
	getJSON(t, server, "/api/system")
	api.cloud.inflight.Wait()
	_, body = getJSON(t, server, "/api/system")
	cloud, _ = body["cloud"].(map[string]any)
	if cloud["status"] != cloudStatusUnavailable || cloud["available"] != false || cloud["total"] != nil {
		t.Fatalf("unavailable cloud = %v", cloud)
	}
}

// blockingAboutEngine holds operations/about until released, so a test can
// prove that the HTTP handler does not wait for the cloud.
type blockingAboutEngine struct {
	*fakeEngine
	started chan struct{}
	release chan struct{}
}

func (b *blockingAboutEngine) Call(ctx context.Context, method string, in, out any) error {
	if method == "operations/about" {
		select {
		case <-b.started:
		default:
			close(b.started)
		}
		<-b.release
	}
	return b.fakeEngine.Call(ctx, method, in, out)
}

func TestSystemDoesNotBlockOnUpstream(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	fake := &fakeEngine{responses: map[string]any{
		"operations/about": map[string]any{"total": 1000, "used": 400, "free": 600},
	}}
	blocking := &blockingAboutEngine{fakeEngine: fake, started: make(chan struct{}), release: make(chan struct{})}
	manager := jobs.NewManager(st, blocking, jobs.Options{})
	api, err := New(Config{
		Store: st, Rclone: blocking, Jobs: manager,
		Downloads: root, DestRoots: []string{root}, Version: "test",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	// Create an account directly in the store: config/create would also block
	// on nothing, but this keeps the arrange phase off the HTTP path.
	if err := st.AddAccount(store.Account{ID: "acc-1", Name: "Яндекс", Provider: "yandex-token", Remote: "cf_1", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add account: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/system", nil)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("handler waited for the cloud: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}

	select {
	case <-blocking.started:
	case <-time.After(2 * time.Second):
		t.Fatal("background refresh did not start")
	}
	close(blocking.release)
	api.cloud.inflight.Wait()

	_, body = getJSON(t, server, "/api/system")
	cloud, _ := body["cloud"].(map[string]any)
	if cloud["status"] != cloudStatusOK || cloud["total"] != float64(1000) {
		t.Fatalf("cloud after release = %v", cloud)
	}
}
