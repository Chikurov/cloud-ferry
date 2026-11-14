package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloudferry/internal/disk"
	"cloudferry/internal/jobs"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
	"cloudferry/internal/yandex"
)

type recordedCall struct {
	method string
	input  map[string]any
}

type fakeEngine struct {
	mu        sync.Mutex
	calls     []recordedCall
	responses map[string]any
	errors    map[string]error
	statItems map[string]any
	pingErr   error
}

func (f *fakeEngine) Call(_ context.Context, method string, in, out any) error {
	call := recordedCall{method: method}
	if typed, ok := in.(map[string]any); ok {
		call.input = typed
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	failErr := f.errors[method]
	response, ok := f.responses[method]
	if method == "operations/stat" && f.statItems != nil {
		remote, _ := call.input["remote"].(string)
		if item, exists := f.statItems[remote]; exists {
			response, ok = map[string]any{"item": item}, true
		}
	}
	f.mu.Unlock()
	if failErr != nil {
		return failErr
	}
	if !ok || out == nil {
		return nil
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, out)
}

func (f *fakeEngine) setResponse(method string, response any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[method] = response
}

// setStat makes operations/stat answer for one cloud path. A nil item means the
// path does not exist; {"IsDir": ...} tells a folder from a file.
func (f *fakeEngine) setStat(remote string, item map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statItems == nil {
		f.statItems = map[string]any{}
	}
	f.statItems[remote] = item
}

func (f *fakeEngine) setError(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errors == nil {
		f.errors = map[string]error{}
	}
	f.errors[method] = err
}

func (f *fakeEngine) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, call := range f.calls {
		if call.method == method {
			total++
		}
	}
	return total
}

func (f *fakeEngine) Ping(context.Context) (rc.Version, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pingErr != nil {
		return rc.Version{}, f.pingErr
	}
	return rc.Version{Version: "v1.76.0"}, nil
}

func (f *fakeEngine) setPingError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pingErr = err
}

func (f *fakeEngine) find(method string) (recordedCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.method == method {
			return call, true
		}
	}
	return recordedCall{}, false
}

func newTestServer(t *testing.T) (*httptest.Server, *store.Store, *fakeEngine) {
	t.Helper()
	server, st, engine, _, _ := newTestServerWith(t, nil)
	return server, st, engine
}

// newTestServerWith also allows the test to adjust the server config and to
// reach the live Server (to inspect pending device flows) and the state
// directory (to inspect state.json on disk).
func newTestServerWith(t *testing.T, configure func(*Config)) (*httptest.Server, *store.Store, *fakeEngine, *Server, string) {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &fakeEngine{responses: map[string]any{
		"sync/copy":        map[string]any{"jobid": 11},
		"sync/sync":        map[string]any{"jobid": 12},
		"core/stats":       map[string]any{"bytes": 512, "totalBytes": 1024},
		"job/status":       map[string]any{"finished": false},
		"operations/about": map[string]any{"total": 1000, "used": 400, "free": 600},
		"operations/list":  map[string]any{"list": []map[string]any{{"Name": "Фото", "IsDir": true}}},
		"operations/size":  map[string]any{"count": 10, "bytes": 1000},
		"operations/stat":  map[string]any{"item": map[string]any{"IsDir": true}},
	}}
	manager := jobs.NewManager(st, engine, jobs.Options{})
	cfg := Config{
		Store:     st,
		Rclone:    engine,
		Jobs:      manager,
		Downloads: "/downloads",
		Version:   "test",
	}
	if configure != nil {
		configure(&cfg)
	}
	api, err := New(cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	handler := api.Handler()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, st, engine, api, dir
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any) (*http.Response, map[string]any) {
	t.Helper()

	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = strings.NewReader(string(payload))
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

func TestHealthz(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body["ok"] != true || body["rclone"] != "v1.76.0" {
		t.Fatalf("body = %v", body)
	}
}

func TestReadyz(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/readyz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["ok"] != true || body["rclone"] != "v1.76.0" || body["version"] != "test" {
		t.Fatalf("body = %v", body)
	}
}

func TestReadyzWhenRcloneDown(t *testing.T) {
	server, _, engine := newTestServer(t)
	engine.setPingError(errors.New("dial unix /run/rclone/rclone.sock: connect: no such file or directory"))

	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/readyz", nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["ok"] != false || body["error"] == nil {
		t.Fatalf("body = %v", body)
	}
}

// A restarting engine must not make the liveness probe fail: /api/healthz keeps
// answering 200 and reports "ok": false instead.
func TestHealthzStaysLiveWhenRcloneDown(t *testing.T) {
	server, _, engine := newTestServer(t)
	engine.setPingError(errors.New("dial unix /run/rclone/rclone.sock: connect: connection refused"))

	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body["ok"] != false {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateWebDAVAccount(t *testing.T) {
	server, st, engine := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name":     "Яндекс",
		"provider": "webdav",
		"url":      "https://webdav.yandex.ru",
		"user":     "user@yandex.ru",
		"password": "app-password",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}

	call, ok := engine.find("config/create")
	if !ok {
		t.Fatal("config/create was not called")
	}
	params, _ := call.input["parameters"].(map[string]any)
	if params["vendor"] != "other" || params["url"] != "https://webdav.yandex.ru" {
		t.Fatalf("parameters = %v", params)
	}
	opt, _ := call.input["opt"].(map[string]any)
	if opt["obscure"] != true || opt["nonInteractive"] != true {
		t.Fatalf("opt = %v", opt)
	}

	if accounts := st.Accounts(); len(accounts) != 1 || !strings.HasPrefix(accounts[0].Remote, "cf_") {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestCreateAccountValidation(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"provider": "webdav",
		"url":      "https://webdav.yandex.ru",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestProbe(t *testing.T) {
	server, _, _ := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)
	if accountID == "" {
		t.Fatalf("no account id in %v", created)
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts/"+accountID+"/probe", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	about, _ := body["about"].(map[string]any)
	if about["total"] == nil {
		t.Fatalf("about = %v", body)
	}
}

func TestCreateJobRejectsEscapeFromDestination(t *testing.T) {
	server, _, _ := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID,
		"destPath":  "/etc/passwd",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestCreateJobAndWidget(t *testing.T) {
	server, _, engine := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":  accountID,
		"sourcePath": "/фото",
		"destPath":   "/downloads/yandex",
		"mode":       "copy",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["status"] != store.StatusRunning {
		t.Fatalf("job = %v", body)
	}
	call, ok := engine.find("sync/copy")
	if !ok {
		t.Fatal("sync/copy was not called")
	}
	if call.input["createEmptySrcDirs"] != true {
		t.Fatalf("createEmptySrcDirs must be a top-level rc parameter, input = %v", call.input)
	}
	config, _ := call.input["_config"].(map[string]any)
	if _, exists := config["CreateEmptySrcDirs"]; exists {
		t.Fatalf("CreateEmptySrcDirs is not a valid rclone config option: %v", config)
	}

	_, widget := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/widget", nil)
	if widget["type"] != "text-with-progress" {
		t.Fatalf("widget = %v", widget)
	}
}

func TestIndexPageServed(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, err := server.Client().Get(server.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	if !strings.Contains(string(buf[:n]), "Cloud Ferry") {
		t.Fatalf("index page looks wrong: %q", string(buf[:n]))
	}
}

func TestYandexAuthURL(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/start", map[string]any{
		"clientId": "my-client",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	authURL, _ := body["url"].(string)
	if !strings.Contains(authURL, "/authorize") || !strings.Contains(authURL, "client_id=my-client") {
		t.Fatalf("url = %q", authURL)
	}
}

func TestYandexAuthURLRequiresClientID(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, _ := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/start", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestYandexFinishCreatesAccount(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600}`))
	}))
	defer oauth.Close()
	previous := yandex.Endpoint
	yandex.Endpoint = oauth.URL
	t.Cleanup(func() { yandex.Endpoint = previous })

	server, st, engine := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/finish", map[string]any{
		"name":         "Яндекс",
		"clientId":     "cid",
		"clientSecret": "secret",
		"code":         "1234567",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}

	accounts := st.Accounts()
	if len(accounts) != 1 || accounts[0].Provider != "yandex-oauth" {
		t.Fatalf("accounts = %+v", accounts)
	}

	call, ok := engine.find("config/create")
	if !ok {
		t.Fatal("config/create was not called")
	}
	if call.input["type"] != "yandex" {
		t.Fatalf("type = %v", call.input["type"])
	}
	params, _ := call.input["parameters"].(map[string]any)
	if params["client_id"] != "cid" || params["client_secret"] != "secret" {
		t.Fatalf("parameters = %v", params)
	}
	token, _ := params["token"].(string)
	if !strings.Contains(token, `"access_token":"at"`) {
		t.Fatalf("token = %q", token)
	}
}

func TestPreflight(t *testing.T) {
	server, _, _ := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/preflight", map[string]any{
		"accountId":  accountID,
		"sourcePath": "фото",
		"destPath":   "/downloads/yandex",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["files"] != float64(10) || body["bytes"] != float64(1000) {
		t.Fatalf("body = %v", body)
	}
	if body["fits"] != true {
		t.Fatalf("fits = %v", body["fits"])
	}
}

func TestProbeReportsListingWarning(t *testing.T) {
	server, _, engine := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)
	engine.errors = map[string]error{"operations/list": errors.New("410 Gone")}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts/"+accountID+"/probe", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if warning, _ := body["warning"].(string); warning == "" {
		t.Fatalf("expected warning, body = %v", body)
	}
}

func TestStopReturnsWarningWhenRcloneFails(t *testing.T) {
	server, _, engine := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)
	_, job := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": "/downloads/yandex",
	})
	jobID, _ := job["id"].(string)
	engine.errors = map[string]error{"job/stop": errors.New("rclone is down")}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/"+jobID+"/stop", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if warning, _ := body["warning"].(string); warning == "" {
		t.Fatalf("expected warning, body = %v", body)
	}
}

func TestCannotDeleteAccountWithRunningJob(t *testing.T) {
	server, _, _ := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)
	if resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": "/downloads/a",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("create job: %d %v", resp.StatusCode, body)
	}

	resp, body := doJSON(t, server.Client(), http.MethodDelete, server.URL+"/api/accounts/"+accountID, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestCannotStartTwoJobsIntoOneDestination(t *testing.T) {
	server, _, _ := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)
	payload := map[string]any{"accountId": accountID, "destPath": "/downloads/same"}
	if resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", payload); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first job: %d %v", resp.StatusCode, body)
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", payload)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestSystemReportsFreeSpace(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/system", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body["downloads"] != "/downloads" {
		t.Fatalf("downloads = %v", body["downloads"])
	}
	roots, _ := body["roots"].([]any)
	if len(roots) == 0 {
		t.Fatalf("roots = %v", body["roots"])
	}
}

func TestDestinationPathValidation(t *testing.T) {
	cases := []struct {
		name     string
		dest     string
		source   string
		expected int
	}{
		{"inside downloads", "/downloads/photo", "", http.StatusCreated},
		{"downloads root", "/downloads", "", http.StatusCreated},
		{"escape with dots", "/downloads/../etc", "", http.StatusBadRequest},
		{"relative path", "downloads/photo", "", http.StatusBadRequest},
		{"outside root", "/etc/passwd", "", http.StatusBadRequest},
		{"source traversal", "/downloads/photo", "../secret", http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Each case gets its own server: a job started into /downloads/photo
			// would legitimately conflict with a later one into /downloads.
			server, _, _ := newTestServer(t)
			accountID := createTestAccount(t, server)
			resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
				"accountId": accountID, "destPath": tc.dest, "sourcePath": tc.source,
			})
			if resp.StatusCode != tc.expected {
				t.Fatalf("status = %d, want %d, body = %v", resp.StatusCode, tc.expected, body)
			}
		})
	}
}

func TestStateReturnsNewestJobFirst(t *testing.T) {
	server, st, _ := newTestServer(t)
	if err := st.AddJob(store.Job{ID: "old", Status: store.StatusDone, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("add old: %v", err)
	}
	if err := st.AddJob(store.Job{ID: "new", Status: store.StatusDone, CreatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("add new: %v", err)
	}

	_, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/state", nil)
	jobs, _ := body["jobs"].([]any)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %v", body["jobs"])
	}
	first, _ := jobs[0].(map[string]any)
	if first["id"] != "new" {
		t.Fatalf("first job = %v", first["id"])
	}
}

func TestUnknownJobActionsReturn404(t *testing.T) {
	server, _, _ := newTestServer(t)
	for _, path := range []string{"/api/jobs/nope/stop", "/api/jobs/nope/resume", "/api/jobs/nope/check"} {
		resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+path, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status = %d, body = %v", path, resp.StatusCode, body)
		}
	}
}

func TestAccountNameIsCapped(t *testing.T) {
	server, _, _ := newTestServer(t)
	long := strings.Repeat("Очень длинное название облака ", 5)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": long, "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	name, _ := body["name"].(string)
	if len([]rune(name)) > 64 {
		t.Fatalf("name length = %d", len([]rune(name)))
	}
}

func TestYandexFinishMapsOAuthErrors(t *testing.T) {
	cases := []struct {
		code     string
		expected string
	}{
		{"invalid_grant", "Код не подошёл или уже истёк. Получите новый код и попробуйте снова."},
		{"invalid_client", "ID или секрет приложения не подошли. Проверьте их в настройках приложения на oauth.yandex.ru."},
		{"unauthorized_client", "У приложения не включён вход через Яндекс ID. Включите его в настройках приложения."},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"` + tc.code + `","error_description":"internal english text"}`))
			}))
			defer oauth.Close()
			previous := yandex.Endpoint
			yandex.Endpoint = oauth.URL
			t.Cleanup(func() { yandex.Endpoint = previous })

			server, _, _ := newTestServer(t)
			resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/finish", map[string]any{
				"clientId": "cid", "clientSecret": "secret", "code": "1234567",
			})
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
			}
			if body["error"] != tc.expected {
				t.Fatalf("error = %v, want %q", body["error"], tc.expected)
			}
		})
	}
}

func TestYandexFinishAcceptsUIFrontendPayload(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600}`))
	}))
	defer oauth.Close()
	previous := yandex.Endpoint
	yandex.Endpoint = oauth.URL
	t.Cleanup(func() { yandex.Endpoint = previous })

	server, _, _ := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/finish", map[string]any{
		"name":         "Яндекс",
		"provider":     "yandex-oauth",
		"clientId":     "cid",
		"clientSecret": "secret",
		"code":         "1234567",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestYandexFinishRejectsMalformedCode(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/finish", map[string]any{
		"clientId": "cid", "code": "12ab",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestCreateJobUnknownModeReturns400(t *testing.T) {
	server, _, _ := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": "/downloads/x", "mode": "move",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestSyncAtDownloadsRootRejected(t *testing.T) {
	server, _, _ := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": "/downloads", "mode": "sync",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}

	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": "/downloads/photo", "mode": "sync",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("subdir: status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestStopFinishedJobReturnsConflict(t *testing.T) {
	server, st, _ := newTestServer(t)
	if err := st.AddJob(store.Job{ID: "done", Status: store.StatusDone, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("add: %v", err)
	}
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/done/stop", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	stored, _ := st.Job("done")
	if stored.Status != store.StatusDone {
		t.Fatalf("status = %q", stored.Status)
	}
}

func TestResumeRunningJobReturnsConflict(t *testing.T) {
	server, _, _ := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)
	_, job := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": "/downloads/one",
	})
	jobID, _ := job["id"].(string)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/"+jobID+"/resume", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestConcurrentCreateJobSameDestOneConflict(t *testing.T) {
	server, _, _ := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)

	type result struct{ status int }
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			resp, _ := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
				"accountId": accountID, "destPath": "/downloads/race",
			})
			results <- result{resp.StatusCode}
		}()
	}
	created201, conflict409 := 0, 0
	for i := 0; i < 2; i++ {
		switch (<-results).status {
		case http.StatusCreated:
			created201++
		case http.StatusConflict:
			conflict409++
		}
	}
	if created201 != 1 || conflict409 != 1 {
		t.Fatalf("created=%d conflict=%d", created201, conflict409)
	}
}

func TestMutatingRejectsNonJSON(t *testing.T) {
	server, _, _ := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/preflight", strings.NewReader(`{"x":1}`))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, err := server.Client().Get(server.URL + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("X-Frame-Options = %q", resp.Header.Get("X-Frame-Options"))
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("CSP = %q", resp.Header.Get("Content-Security-Policy"))
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff missing")
	}
}

func TestBodyTooLargeIs413(t *testing.T) {
	server, _, _ := newTestServer(t)
	huge := `{"name":"` + strings.Repeat("a", 1<<20) + `"}`
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", json.RawMessage(huge))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestTrailingJSONRejected(t *testing.T) {
	server, _, _ := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/oauth/yandex/start",
		strings.NewReader(`{"clientId":"a"}{"evil":1}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestYandexTokenValidation(t *testing.T) {
	server, _, engine := newTestServer(t)
	resp, _ := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token", "token": "not-json",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid token status = %d", resp.StatusCode)
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс", "provider": "yandex-token",
		"token": `{"access_token":"x"}`, "clientId": "cid", "clientSecret": "s",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("valid token status = %d, body = %v", resp.StatusCode, body)
	}
	call, _ := engine.find("config/create")
	params, _ := call.input["parameters"].(map[string]any)
	if params["client_id"] != "cid" || params["client_secret"] != "s" {
		t.Fatalf("parameters = %v", params)
	}
}

func TestBasicAuthProtectsStandaloneInstall(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &fakeEngine{responses: map[string]any{}}
	api, err := New(Config{
		Store: st, Rclone: engine, Jobs: jobs.NewManager(st, engine, jobs.Options{}),
		Downloads: "/downloads", Version: "test", BasicUser: "admin", BasicPass: "secret",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/api/state")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/state", nil)
	req.SetBasicAuth("admin", "secret")
	authorized, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer authorized.Body.Close()
	if authorized.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d", authorized.StatusCode)
	}
}

// fakeYandex emulates Yandex's device flow and records the client credentials
// of every request, so tests can prove which OAuth app the server used.
type fakeYandex struct {
	mu      sync.Mutex
	device  []url.Values
	token   []url.Values
	pending int
}

// newFakeYandex points the yandex package at a local fake. pending is how many
// token polls answer "authorization_pending" before the token is issued.
func newFakeYandex(t *testing.T, expiresIn, pending int) *fakeYandex {
	t.Helper()
	fake := &fakeYandex{pending: pending}
	var deviceCodes int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/device/code":
			fake.mu.Lock()
			fake.device = append(fake.device, r.PostForm)
			fake.mu.Unlock()
			code := atomic.AddInt32(&deviceCodes, 1)
			fmt.Fprintf(w, `{"device_code":"dc-%d","user_code":"h5nbcr6c","verification_url":"https://oauth.yandex.ru/device","interval":1,"expires_in":%d}`, code, expiresIn)
		case "/token":
			fake.mu.Lock()
			fake.token = append(fake.token, r.PostForm)
			isPending := fake.pending > 0
			if isPending {
				fake.pending--
			}
			fake.mu.Unlock()
			if isPending {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending","error_description":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600}`))
		default:
			t.Errorf("unexpected Yandex path %s", r.URL.Path)
		}
	}))
	previous := yandex.Endpoint
	yandex.Endpoint = server.URL
	t.Cleanup(func() {
		yandex.Endpoint = previous
		server.Close()
	})
	return fake
}

func (f *fakeYandex) deviceForms() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.device...)
}

func (f *fakeYandex) tokenForms() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]url.Values(nil), f.token...)
}

const (
	testYandexClientID     = "server-client-id"
	testYandexClientSecret = "server-client-secret"
)

func withServerYandexCredentials(cfg *Config) {
	cfg.YandexClientID = testYandexClientID
	cfg.YandexClientSecret = testYandexClientSecret
}

// startDeviceFlow starts a flow and returns its id and the decoded response.
func startDeviceFlow(t *testing.T, server *httptest.Server, body any) (string, map[string]any) {
	t.Helper()
	resp, decoded := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/device/start", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("device start status = %d, body = %v", resp.StatusCode, decoded)
	}
	flowID, _ := decoded["flowId"].(string)
	if flowID == "" {
		t.Fatalf("device start has no flowId: %v", decoded)
	}
	return flowID, decoded
}

func TestYandexStatusWithoutServerCredentials(t *testing.T) {
	server, _, _, _, _ := newTestServerWith(t, nil)

	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/status", nil)
	if resp.StatusCode != http.StatusOK || body["available"] != false {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestYandexStatusWithServerCredentials(t *testing.T) {
	server, _, _, _, _ := newTestServerWith(t, withServerYandexCredentials)

	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/status", nil)
	if resp.StatusCode != http.StatusOK || body["available"] != true {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if len(body) != 1 {
		t.Fatalf("status must expose only availability, body = %v", body)
	}
}

func TestYandexStatusNeedsBothCredentials(t *testing.T) {
	server, _, _, _, _ := newTestServerWith(t, func(cfg *Config) {
		cfg.YandexClientID = testYandexClientID
	})

	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/status", nil)
	if resp.StatusCode != http.StatusOK || body["available"] != false {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestDeviceFlowUsesServerCredentials(t *testing.T) {
	fake := newFakeYandex(t, 300, 1)
	server, st, engine, _, dir := newTestServerWith(t, withServerYandexCredentials)

	// No client credentials in the body: the server's own app is used.
	flowID, start := startDeviceFlow(t, server, map[string]any{"name": "Яндекс"})
	if start["userCode"] != "h5nbcr6c" || start["verificationUrl"] == "" {
		t.Fatalf("start body = %v", start)
	}

	device := fake.deviceForms()
	if len(device) != 1 || device[0].Get("client_id") != testYandexClientID {
		t.Fatalf("device/code did not use the server app: %v", device)
	}
	if device[0].Get("client_secret") != "" {
		t.Fatalf("device/code must not carry the client secret: %v", device[0])
	}

	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/device/"+flowID, nil)
	if resp.StatusCode != http.StatusOK || body["status"] != "pending" {
		t.Fatalf("pending: %d %v", resp.StatusCode, body)
	}
	resp, body = doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/device/"+flowID, nil)
	if resp.StatusCode != http.StatusOK || body["status"] != "done" {
		t.Fatalf("done: %d %v", resp.StatusCode, body)
	}

	tokens := fake.tokenForms()
	if len(tokens) != 2 {
		t.Fatalf("token polls = %d, want 2", len(tokens))
	}
	for _, form := range tokens {
		if form.Get("client_id") != testYandexClientID || form.Get("client_secret") != testYandexClientSecret {
			t.Fatalf("token poll must use the server app, form = %v", form)
		}
		if form.Get("grant_type") != "device_code" || form.Get("code") == "" {
			t.Fatalf("token poll form = %v", form)
		}
	}

	accounts := st.Accounts()
	if len(accounts) != 1 || accounts[0].Provider != "yandex-oauth" {
		t.Fatalf("accounts = %+v", accounts)
	}

	call, ok := engine.find("config/create")
	if !ok {
		t.Fatal("config/create was not called")
	}
	params, _ := call.input["parameters"].(map[string]any)
	if params["client_id"] != testYandexClientID {
		t.Fatalf("remote client_id = %v, want the server app", params["client_id"])
	}
	if params["client_secret"] != testYandexClientSecret {
		t.Fatalf("remote client_secret is missing: rclone could not refresh the token, params = %v", params)
	}
	tokenJSON, _ := params["token"].(string)
	for _, want := range []string{`"access_token":"at"`, `"refresh_token":"rt"`, `"expiry"`} {
		if !strings.Contains(tokenJSON, want) {
			t.Fatalf("token %q is missing %s", tokenJSON, want)
		}
	}

	// The account record on disk never keeps the OAuth app secret.
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	if strings.Contains(string(raw), testYandexClientSecret) {
		t.Fatalf("client secret leaked into state.json: %s", raw)
	}
}

func TestDeviceFlowServerCredentialsTakePriority(t *testing.T) {
	fake := newFakeYandex(t, 300, 0)
	server, _, _, _, _ := newTestServerWith(t, withServerYandexCredentials)

	startDeviceFlow(t, server, map[string]any{
		"name": "Яндекс", "clientId": "body-client-id", "clientSecret": "body-client-secret",
	})

	device := fake.deviceForms()
	if len(device) != 1 || device[0].Get("client_id") != testYandexClientID {
		t.Fatalf("body credentials must not override the server app: %v", device)
	}
}

func TestDeviceFlowRequiresCredentialsWithoutServerConfig(t *testing.T) {
	fake := newFakeYandex(t, 300, 0)
	server, _, _, _, _ := newTestServerWith(t, nil)

	for _, body := range []map[string]any{
		{},
		{"clientId": "cid"},
		{"clientSecret": "secret"},
	} {
		resp, decoded := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/device/start", body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %v: status = %d, want 400", body, resp.StatusCode)
		}
		if decoded["error"] == nil {
			t.Fatalf("body %v: no error message: %v", body, decoded)
		}
	}
	if got := len(fake.deviceForms()); got != 0 {
		t.Fatalf("rejected starts reached Yandex %d times", got)
	}

	// Explicit body credentials still work when the server has no own app.
	startDeviceFlow(t, server, map[string]any{"clientId": "body-cid", "clientSecret": "body-secret"})
	device := fake.deviceForms()
	if len(device) != 1 || device[0].Get("client_id") != "body-cid" {
		t.Fatalf("device/code forms = %v", device)
	}
}

func TestDeviceFlowRejectsTooManyFlows(t *testing.T) {
	fake := newFakeYandex(t, 300, 0)
	server, _, _, api, _ := newTestServerWith(t, withServerYandexCredentials)

	for i := 0; i < maxDeviceFlows; i++ {
		startDeviceFlow(t, server, nil)
	}
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/device/start", nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("over-limit status = %d, body = %v", resp.StatusCode, body)
	}
	if got := len(fake.deviceForms()); got != maxDeviceFlows {
		t.Fatalf("the rejected start must not reach Yandex: %d device/code calls", got)
	}

	// Expired flows are pruned, so they do not hold their slots forever.
	api.flowsMu.Lock()
	for _, flow := range api.flows {
		flow.expiresAt = time.Now().Add(-time.Minute)
	}
	api.flowsMu.Unlock()

	startDeviceFlow(t, server, nil)
	if got := len(fake.deviceForms()); got != maxDeviceFlows+1 {
		t.Fatalf("device/code calls after pruning = %d, want %d", got, maxDeviceFlows+1)
	}
}

func TestDeviceFlowExpires(t *testing.T) {
	newFakeYandex(t, 1800, 1) // Yandex offers half an hour; the server caps it
	server, _, _, api, _ := newTestServerWith(t, withServerYandexCredentials)

	flowID, _ := startDeviceFlow(t, server, nil)

	api.flowsMu.Lock()
	flow, ok := api.flows[flowID]
	var expiresAt time.Time
	if ok {
		expiresAt = flow.expiresAt
	}
	api.flowsMu.Unlock()
	if !ok {
		t.Fatalf("flow %q was not recorded", flowID)
	}
	remaining := time.Until(expiresAt)
	if remaining > deviceFlowMaxLifetime+time.Second || remaining < deviceFlowMaxLifetime-time.Minute {
		t.Fatalf("flow lifetime = %v, want the %v cap", remaining, deviceFlowMaxLifetime)
	}

	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/device/"+flowID, nil)
	if resp.StatusCode != http.StatusOK || body["status"] != "pending" {
		t.Fatalf("live flow: %d %v", resp.StatusCode, body)
	}

	api.flowsMu.Lock()
	api.flows[flowID].expiresAt = time.Now().Add(-time.Second)
	api.flowsMu.Unlock()

	resp, _ = doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/device/"+flowID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expired flow status = %d, want 404", resp.StatusCode)
	}
}

func TestDeviceFlowDoesNotLeakServerSecret(t *testing.T) {
	var buf bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	newFakeYandex(t, 300, 0)
	server, _, _, _, _ := newTestServerWith(t, withServerYandexCredentials)

	resp, start := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/device/start", map[string]any{"name": "Яндекс"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, body = %v", resp.StatusCode, start)
	}
	flowID, _ := start["flowId"].(string)

	resp, done := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/device/"+flowID, nil)
	if resp.StatusCode != http.StatusOK || done["status"] != "done" {
		t.Fatalf("done: %d %v", resp.StatusCode, done)
	}

	for name, payload := range map[string]map[string]any{"start": start, "done": done} {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if strings.Contains(string(encoded), testYandexClientSecret) {
			t.Fatalf("client secret leaked into the %s response: %s", name, encoded)
		}
	}
	logs := buf.String()
	if strings.Contains(logs, testYandexClientSecret) {
		t.Fatalf("client secret leaked into the log: %s", logs)
	}
	if !strings.Contains(logs, "client id "+testYandexClientID) {
		t.Fatalf("log should name only the public client id: %q", logs)
	}
}

func TestDeviceFlowConnectsAccount(t *testing.T) {
	fake := newFakeYandex(t, 300, 1)
	server, st, engine := newTestServer(t)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/oauth/yandex/device/start", map[string]any{
		"name": "Яндекс", "clientId": "cid", "clientSecret": "secret",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, body = %v", resp.StatusCode, body)
	}
	flowID, _ := body["flowId"].(string)
	if flowID == "" || body["userCode"] != "h5nbcr6c" {
		t.Fatalf("start body = %v", body)
	}
	// Without its own app the server still accepts credentials from the body.
	if forms := fake.deviceForms(); len(forms) != 1 || forms[0].Get("client_id") != "cid" {
		t.Fatalf("device/code forms = %v", forms)
	}

	resp, body = doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/device/"+flowID, nil)
	if resp.StatusCode != http.StatusOK || body["status"] != "pending" {
		t.Fatalf("pending: %d %v", resp.StatusCode, body)
	}

	resp, body = doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/device/"+flowID, nil)
	if resp.StatusCode != http.StatusOK || body["status"] != "done" {
		t.Fatalf("done: %d %v", resp.StatusCode, body)
	}
	if accounts := st.Accounts(); len(accounts) != 1 || accounts[0].Provider != "yandex-oauth" {
		t.Fatalf("accounts = %+v", accounts)
	}
	if _, ok := engine.find("config/create"); !ok {
		t.Fatal("config/create was not called")
	}

	// The flow is single-use.
	resp, _ = doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/device/"+flowID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reused flow status = %d", resp.StatusCode)
	}
}

func TestDeviceFlowRejectsUnknownFlow(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, _ := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/oauth/yandex/device/nope", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestCreateJobWithSourcePaths(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": []string{"Документы", "Фотографии/2024"},
		"destPath":    "/downloads/yandex",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	paths, _ := body["sourcePaths"].([]any)
	if len(paths) != 2 || paths[0] != "Документы" || paths[1] != "Фотографии/2024" {
		t.Fatalf("sourcePaths = %v", body["sourcePaths"])
	}
	if source, _ := body["source"].(string); source == "" || !strings.HasSuffix(source, ":") {
		t.Fatalf("source = %q, want <remote>:", source)
	}

	call, ok := engine.find("sync/copy")
	if !ok {
		t.Fatal("sync/copy was not called")
	}
	if call.input["srcFs"] != body["source"] {
		t.Fatalf("srcFs = %v, body source = %v", call.input["srcFs"], body["source"])
	}
	filter, ok := call.input["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call.input)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if len(rules) != 2 || rules[0] != "/Документы/**" || rules[1] != "/Фотографии/2024/**" {
		t.Fatalf("rules = %v", filter["IncludeRule"])
	}

	jobID, _ := body["id"].(string)
	stored, ok := st.Job(jobID)
	if !ok || len(stored.SourcePaths) != 2 {
		t.Fatalf("stored job = %+v", stored)
	}
}

func TestCreateJobSourcePathsNormalised(t *testing.T) {
	server, _, _ := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": []string{" B/", "A", "A/sub", "A", "/B/"},
		"destPath":    "/downloads/norm",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	paths, _ := body["sourcePaths"].([]any)
	if len(paths) != 2 || paths[0] != "A" || paths[1] != "B" {
		t.Fatalf("sourcePaths = %v, want [A B]", body["sourcePaths"])
	}
}

func TestCreateJobRejectsBothSourceFields(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePath":  "Фото",
		"sourcePaths": []string{"Документы"},
		"destPath":    "/downloads/x",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["field"] != "sourcePaths" {
		t.Fatalf("field = %v", body["field"])
	}
	if engine.count("sync/copy") != 0 {
		t.Fatal("sync/copy must not start on a bad request")
	}
}

func TestCreateJobSourcePathsLimit(t *testing.T) {
	server, _, _ := newTestServer(t)
	accountID := createTestAccount(t, server)

	paths := make([]string, 0, 21)
	for i := 0; i < 21; i++ {
		paths = append(paths, fmt.Sprintf("папка-%02d", i))
	}
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": paths[:20],
		"destPath":    "/downloads/limit-ok",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("20 folders: status = %d, body = %v", resp.StatusCode, body)
	}

	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": paths[:21],
		"destPath":    "/downloads/limit-too-many",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("21 folders: status = %d, body = %v", resp.StatusCode, body)
	}
	if body["field"] != "sourcePaths" {
		t.Fatalf("field = %v", body["field"])
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "не больше 20 папок") {
		t.Fatalf("error = %q", message)
	}
}

func TestCreateJobSourcePathsRejectsBadPaths(t *testing.T) {
	server, _, _ := newTestServer(t)
	accountID := createTestAccount(t, server)

	cases := []struct {
		name  string
		paths []string
	}{
		{"traversal", []string{"../secret"}},
		{"root itself", []string{"."}},
		{"nested traversal", []string{"a/../../b"}},
		{"empty item", []string{""}},
		{"control characters", []string{"фото\nновое"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
				"accountId":   accountID,
				"sourcePaths": tc.paths,
				"destPath":    "/downloads/bad",
			})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
			}
			if body["field"] != "sourcePaths" {
				t.Fatalf("field = %v", body["field"])
			}
			if message, _ := body["error"].(string); message == "" {
				t.Fatalf("empty error: %v", body)
			}
		})
	}
}

func TestCreateJobEmptySourcePathsMeansWholeDisk(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": []string{},
		"destPath":    "/downloads/all",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	source, _ := body["source"].(string)
	if source == "" || !strings.HasSuffix(source, ":") {
		t.Fatalf("source = %q", source)
	}
	call, _ := engine.find("sync/copy")
	if call.input["srcFs"] != source {
		t.Fatalf("srcFs = %v", call.input["srcFs"])
	}
	if _, exists := call.input["_filter"]; exists {
		t.Fatalf("whole disk must not carry a filter: %#v", call.input)
	}
	jobID, _ := body["id"].(string)
	stored, _ := st.Job(jobID)
	if stored.SourcePaths != nil {
		t.Fatalf("sourcePaths = %#v, want nil", stored.SourcePaths)
	}
}

func TestCreateJobWithSourceFiles(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setStat("Фото", map[string]any{"IsDir": true})
	engine.setStat("a.txt", map[string]any{"IsDir": false})
	engine.setStat("docs/b.md", map[string]any{"IsDir": false})

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": []string{"Фото"},
		"sourceFiles": []string{"docs/b.md", "a.txt"},
		"destPath":    "/downloads/files",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	files, _ := body["sourceFiles"].([]any)
	if len(files) != 2 || files[0] != "a.txt" || files[1] != "docs/b.md" {
		t.Fatalf("sourceFiles = %v, want [a.txt docs/b.md]", body["sourceFiles"])
	}
	paths, _ := body["sourcePaths"].([]any)
	if len(paths) != 1 || paths[0] != "Фото" {
		t.Fatalf("sourcePaths = %v", body["sourcePaths"])
	}
	source, _ := body["source"].(string)
	if source == "" || !strings.HasSuffix(source, ":") {
		t.Fatalf("source = %q, want <remote>:", source)
	}

	call, ok := engine.find("sync/copy")
	if !ok {
		t.Fatal("sync/copy was not called")
	}
	if call.input["srcFs"] != source {
		t.Fatalf("srcFs = %v, body source = %v", call.input["srcFs"], source)
	}
	filter, ok := call.input["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call.input)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if len(rules) != 3 || rules[0] != "/Фото/**" || rules[1] != "/a.txt" || rules[2] != "/docs/b.md" {
		t.Fatalf("rules = %v", filter["IncludeRule"])
	}
	if got := engine.count("operations/stat"); got != 3 {
		t.Fatalf("operations/stat calls = %d, want 3 (folder + files)", got)
	}

	jobID, _ := body["id"].(string)
	stored, ok := st.Job(jobID)
	if !ok || len(stored.SourceFiles) != 2 || len(stored.SourcePaths) != 1 {
		t.Fatalf("stored job = %+v", stored)
	}
}

func TestCreateJobOnlyFiles(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setStat("a.txt", map[string]any{"IsDir": false})
	engine.setStat("b/c.md", map[string]any{"IsDir": false})

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": []string{"a.txt", "b/c.md"},
		"destPath":    "/downloads/only-files",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if source, _ := body["source"].(string); !strings.HasSuffix(source, ":") {
		t.Fatalf("source = %q", source)
	}
	if _, exists := body["sourcePaths"]; exists {
		t.Fatalf("sourcePaths must be omitted for a file-only job: %v", body["sourcePaths"])
	}
	call, _ := engine.find("sync/copy")
	filter, ok := call.input["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call.input)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if len(rules) != 2 || rules[0] != "/a.txt" || rules[1] != "/b/c.md" {
		t.Fatalf("rules = %v", filter["IncludeRule"])
	}
	if got := engine.count("operations/stat"); got != 2 {
		t.Fatalf("operations/stat calls = %d, want 2 (only files, no folder)", got)
	}
	jobID, _ := body["id"].(string)
	stored, _ := st.Job(jobID)
	if stored.SourcePaths != nil || len(stored.SourceFiles) != 2 {
		t.Fatalf("stored job = %+v", stored)
	}
}

func TestCreateJobSourceFilesNormalised(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setStat("A/f.txt", map[string]any{"IsDir": false})
	engine.setStat("B/g.txt", map[string]any{"IsDir": false})

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": []string{" B/g.txt/", "A/f.txt", "A/f.txt"},
		"destPath":    "/downloads/files-norm",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	files, _ := body["sourceFiles"].([]any)
	if len(files) != 2 || files[0] != "A/f.txt" || files[1] != "B/g.txt" {
		t.Fatalf("sourceFiles = %v, want [A/f.txt B/g.txt]", body["sourceFiles"])
	}
	if got := engine.count("operations/stat"); got != 2 {
		t.Fatalf("operations/stat calls = %d, want 2 after dedupe", got)
	}
}

func TestCreateJobSourceFilesLimit(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	files := make([]string, 0, 101)
	for i := 0; i < 101; i++ {
		file := fmt.Sprintf("f-%03d.txt", i)
		files = append(files, file)
		engine.setStat(file, map[string]any{"IsDir": false})
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": files[:100],
		"destPath":    "/downloads/files-100",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("100 files: status = %d, body = %v", resp.StatusCode, body)
	}

	// A duplicate in an over-limit request collapses before the limit is
	// checked, so 101 entries with two equal paths still fit.
	withDup := append(append([]string(nil), files[:100]...), files[0])
	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": withDup,
		"destPath":    "/downloads/files-dedup",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("duplicate at the limit: status = %d, body = %v", resp.StatusCode, body)
	}

	statCalls := engine.count("operations/stat")
	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": files,
		"destPath":    "/downloads/files-101",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("101 files: status = %d, body = %v", resp.StatusCode, body)
	}
	if body["field"] != "sourceFiles" {
		t.Fatalf("field = %v", body["field"])
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "не больше 100 файлов") {
		t.Fatalf("error = %q", message)
	}
	if got := engine.count("operations/stat"); got != statCalls {
		t.Fatalf("operations/stat calls = %d, want %d: an over-limit request must not verify", got, statCalls)
	}
}

func TestCreateJobFoldersAndFilesAtLimits(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	folders := make([]string, 0, 20)
	files := make([]string, 0, 100)
	for i := 0; i < 20; i++ {
		folder := fmt.Sprintf("папка-%02d", i)
		folders = append(folders, folder)
		engine.setStat(folder, map[string]any{"IsDir": true})
	}
	for i := 0; i < 100; i++ {
		file := fmt.Sprintf("файл-%03d.txt", i)
		files = append(files, file)
		engine.setStat(file, map[string]any{"IsDir": false})
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": folders,
		"sourceFiles": files,
		"destPath":    "/downloads/mixed-limits",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	call, _ := engine.find("sync/copy")
	filter, ok := call.input["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call.input)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if len(rules) != 120 {
		t.Fatalf("rules = %d, want 120", len(rules))
	}
	if got := engine.count("operations/stat"); got != 120 {
		t.Fatalf("operations/stat calls = %d, want 120", got)
	}
}

func TestCreateJobSourceFilesRejectBadPaths(t *testing.T) {
	server, _, _ := newTestServer(t)
	accountID := createTestAccount(t, server)

	cases := []struct {
		name  string
		files []string
	}{
		{"traversal", []string{"../secret.txt"}},
		{"root itself", []string{"."}},
		{"nested traversal", []string{"a/../../b.txt"}},
		{"empty item", []string{""}},
		{"too long", []string{strings.Repeat("a", maxSourcePathLen+1) + ".txt"}},
		{"control characters", []string{"фото\nновое.txt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
				"accountId":   accountID,
				"sourceFiles": tc.files,
				"destPath":    "/downloads/bad-file",
			})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
			}
			if body["field"] != "sourceFiles" {
				t.Fatalf("field = %v", body["field"])
			}
			if message, _ := body["error"].(string); message == "" {
				t.Fatalf("empty error: %v", body)
			}
		})
	}
}

func TestCreateJobMissingFile(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setStat("gone.txt", nil)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": []string{"gone.txt"},
		"destPath":    "/downloads/missing-file",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "Файл не найден") || !strings.Contains(message, "«gone.txt»") {
		t.Fatalf("error = %q", message)
	}
	if body["field"] != "sourceFiles" {
		t.Fatalf("field = %v", body["field"])
	}
	if engine.count("sync/copy") != 0 {
		t.Fatal("sync/copy must not start for a missing file")
	}
	if len(st.Jobs()) != 0 {
		t.Fatalf("job persisted: %+v", st.Jobs())
	}
}

func TestCreateJobFolderInsteadOfFile(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setStat("Фото", map[string]any{"IsDir": true})

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": []string{"Фото"},
		"destPath":    "/downloads/folder-as-file",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "это папка, а не файл") || !strings.Contains(message, "«Фото»") {
		t.Fatalf("error = %q", message)
	}
	if body["field"] != "sourceFiles" {
		t.Fatalf("field = %v", body["field"])
	}
	if len(st.Jobs()) != 0 {
		t.Fatalf("job persisted: %+v", st.Jobs())
	}
}

func TestCreateJobSourceFilesOutageIs502(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setError("operations/stat", errors.New("rclone is down"))

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": []string{"a.txt"},
		"destPath":    "/downloads/file-outage",
	})
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); message == "" {
		t.Fatalf("empty error: %v", body)
	}
}

func TestCreateJobSyncWithFilesRejected(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": []string{"a.txt"},
		"destPath":    "/downloads/sync-files",
		"mode":        "sync",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "только в режиме «Добавлять»") {
		t.Fatalf("error = %q", message)
	}
	if body["field"] != "mode" {
		t.Fatalf("field = %v", body["field"])
	}
	if engine.count("operations/stat") != 0 || engine.count("sync/sync") != 0 {
		t.Fatalf("sync+files must not verify or launch: stat=%d sync=%d",
			engine.count("operations/stat"), engine.count("sync/sync"))
	}
	if len(st.Jobs()) != 0 {
		t.Fatalf("job persisted: %+v", st.Jobs())
	}
}

func TestCreateJobLegacyPathWithFilesRejected(t *testing.T) {
	server, _, _ := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePath":  "Фото",
		"sourceFiles": []string{"a.txt"},
		"destPath":    "/downloads/legacy-files",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["field"] != "sourceFiles" {
		t.Fatalf("field = %v", body["field"])
	}
}

func TestCreateJobEmptySourceFilesMeansWholeDisk(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourceFiles": []string{},
		"destPath":    "/downloads/files-empty",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if source, _ := body["source"].(string); !strings.HasSuffix(source, ":") {
		t.Fatalf("source = %q", source)
	}
	call, _ := engine.find("sync/copy")
	if _, exists := call.input["_filter"]; exists {
		t.Fatalf("whole disk must not carry a filter: %#v", call.input)
	}
	jobID, _ := body["id"].(string)
	stored, _ := st.Job(jobID)
	if stored.SourceFiles != nil || stored.SourcePaths != nil {
		t.Fatalf("stored job = %+v", stored)
	}
}

func TestPreflightWithSourceFiles(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/preflight", map[string]any{
		"accountId":   accountID,
		"sourcePaths": []string{"Документы"},
		"sourceFiles": []string{"a.txt", "b/c.md"},
		"destPath":    "/downloads/yandex",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["files"] != float64(10) || body["bytes"] != float64(1000) {
		t.Fatalf("body = %v", body)
	}
	call, ok := engine.find("operations/size")
	if !ok {
		t.Fatal("operations/size was not called")
	}
	if fsPath, _ := call.input["fs"].(string); !strings.HasSuffix(fsPath, ":") {
		t.Fatalf("fs = %v, want <remote>:", call.input["fs"])
	}
	filter, ok := call.input["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call.input)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if len(rules) != 3 || rules[0] != "/Документы/**" || rules[1] != "/a.txt" || rules[2] != "/b/c.md" {
		t.Fatalf("rules = %v", filter["IncludeRule"])
	}
}

func TestResumeWithSourceFilesKeepsRules(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setStat("a.txt", map[string]any{"IsDir": false})
	if err := st.AddJob(store.Job{
		ID: "paused-files", AccountID: accountID, Source: "cf_x:",
		SourceFiles: []string{"a.txt"}, Dest: "/downloads/files-resume",
		Status: store.StatusInterrupted, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/paused-files/resume", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	files, _ := body["sourceFiles"].([]any)
	if len(files) != 1 || files[0] != "a.txt" {
		t.Fatalf("sourceFiles = %v", body["sourceFiles"])
	}
	if got := engine.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", got)
	}
	if got := engine.count("operations/stat"); got != 1 {
		t.Fatalf("operations/stat calls = %d, want 1", got)
	}
	call, _ := engine.find("sync/copy")
	filter, ok := call.input["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call.input)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if len(rules) != 1 || rules[0] != "/a.txt" {
		t.Fatalf("rules = %v, want one exact rule", filter["IncludeRule"])
	}

	// A second resume must not start a second transfer for the same files.
	resp, _ = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/paused-files/resume", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second resume status = %d, want 409", resp.StatusCode)
	}
	if got := engine.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls after second resume = %d, want 1", got)
	}
}

func TestResumeMissingFileReturns400(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setStat("gone.txt", nil)
	if err := st.AddJob(store.Job{
		ID: "paused-gone", AccountID: accountID, Source: "cf_x:",
		SourceFiles: []string{"gone.txt"}, Dest: "/downloads/files-gone",
		Status: store.StatusInterrupted, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/paused-gone/resume", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["field"] != "sourceFiles" {
		t.Fatalf("field = %v", body["field"])
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "«gone.txt»") {
		t.Fatalf("error = %q", message)
	}
	stored, _ := st.Job("paused-gone")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted", stored.Status)
	}
	if got := engine.count("sync/copy"); got != 0 {
		t.Fatalf("sync/copy calls = %d, want 0", got)
	}
}

func TestStateExposesSourceFiles(t *testing.T) {
	server, st, _ := newTestServer(t)
	if err := st.AddJob(store.Job{
		ID: "files-state", Source: "cf_x:", SourceFiles: []string{"a.txt", "b/c.md"},
		Dest: "/downloads/yandex", Status: store.StatusDone, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	_, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/state", nil)
	jobs, _ := body["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %v", body["jobs"])
	}
	job, _ := jobs[0].(map[string]any)
	files, _ := job["sourceFiles"].([]any)
	if len(files) != 2 || files[0] != "a.txt" || files[1] != "b/c.md" {
		t.Fatalf("sourceFiles = %v", job["sourceFiles"])
	}
}

func TestCreateJobMissingSourceFolder(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setResponse("operations/stat", map[string]any{"item": nil})

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": []string{"Пропавшая"},
		"destPath":    "/downloads/missing",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "Папка в облаке не найдена") || !strings.Contains(message, "«Пропавшая»") {
		t.Fatalf("error = %q", message)
	}
	if body["field"] != "sourcePaths" {
		t.Fatalf("field = %v", body["field"])
	}
	if engine.count("sync/copy") != 0 {
		t.Fatal("sync/copy must not start for a missing folder")
	}
	if len(st.Jobs()) != 0 {
		t.Fatalf("job persisted: %+v", st.Jobs())
	}
}

func TestCreateJobSourceCheckOutageIs502(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setError("operations/stat", errors.New("rclone is down"))

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": []string{"Фото"},
		"destPath":    "/downloads/outage",
	})
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); message == "" {
		t.Fatalf("empty error: %v", body)
	}
}

func TestPreflightWithSourcePaths(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)

	for _, route := range []string{"/api/preflight", "/api/jobs/preflight"} {
		resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+route, map[string]any{
			"accountId":   accountID,
			"sourcePaths": []string{"Документы", "Фотографии/2024"},
			"destPath":    "/downloads/yandex",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %v", route, resp.StatusCode, body)
		}
		if body["files"] != float64(10) || body["bytes"] != float64(1000) {
			t.Fatalf("%s: body = %v", route, body)
		}
	}
	call, ok := engine.find("operations/size")
	if !ok {
		t.Fatal("operations/size was not called")
	}
	if fsPath, _ := call.input["fs"].(string); !strings.HasSuffix(fsPath, ":") {
		t.Fatalf("fs = %v, want <remote>:", call.input["fs"])
	}
	filter, ok := call.input["_filter"].(map[string]any)
	if !ok {
		t.Fatalf("_filter missing: %#v", call.input)
	}
	rules, _ := filter["IncludeRule"].([]string)
	if len(rules) != 2 || rules[0] != "/Документы/**" {
		t.Fatalf("rules = %v", filter["IncludeRule"])
	}
}

func TestPreflightRejectsBothSourceFields(t *testing.T) {
	server, _, _ := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/preflight", map[string]any{
		"accountId":   accountID,
		"sourcePath":  "Фото",
		"sourcePaths": []string{"Документы"},
		"destPath":    "/downloads/yandex",
	})
	if resp.StatusCode != http.StatusBadRequest || body["field"] != "sourcePaths" {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestSyncAtDownloadsRootRejectedWithSourcePaths(t *testing.T) {
	server, _, _ := newTestServer(t)
	accountID := createTestAccount(t, server)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId":   accountID,
		"sourcePaths": []string{"Фото"},
		"destPath":    "/downloads",
		"mode":        "sync",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestNestedDestinationConflicts(t *testing.T) {
	server, _, _ := newTestServer(t)
	accountID := createTestAccount(t, server)

	if resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": "/downloads/tree",
	}); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first job: %d %v", resp.StatusCode, body)
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": "/downloads/tree/sub",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("nested job: status = %d, body = %v", resp.StatusCode, body)
	}

	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", map[string]any{
		"accountId": accountID, "destPath": "/downloads/tree-other",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sibling job: status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestResumeMissingSourceReturns400(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	if err := st.AddJob(store.Job{
		ID: "paused-multi", AccountID: accountID, Source: "cf_x:",
		SourcePaths: []string{"Фото"}, Dest: "/downloads/yandex",
		Status: store.StatusInterrupted, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	engine.setResponse("operations/stat", map[string]any{"item": nil})

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/paused-multi/resume", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["field"] != "sourcePaths" {
		t.Fatalf("field = %v", body["field"])
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "«Фото»") {
		t.Fatalf("error = %q", message)
	}
	stored, _ := st.Job("paused-multi")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted", stored.Status)
	}
}

func TestStateExposesSourcePaths(t *testing.T) {
	server, st, _ := newTestServer(t)
	if err := st.AddJob(store.Job{
		ID: "multi", Source: "cf_x:", SourcePaths: []string{"Фото"},
		Dest: "/downloads/yandex", Status: store.StatusDone, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}
	_, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/state", nil)
	jobs, _ := body["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %v", body["jobs"])
	}
	job, _ := jobs[0].(map[string]any)
	paths, _ := job["sourcePaths"].([]any)
	if len(paths) != 1 || paths[0] != "Фото" {
		t.Fatalf("sourcePaths = %v", job["sourcePaths"])
	}
}

func TestHumanBytesUnits(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 Б"},
		{1024, "1,0 КБ"},
		{1536, "1,5 КБ"},
		{5 << 30, "5,0 ГБ"},
	}
	for _, tc := range cases {
		if got := humanBytes(tc.n); got != tc.want {
			t.Fatalf("humanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestWidgetFormatsKilobytes(t *testing.T) {
	server, st, _ := newTestServer(t)
	if err := st.AddJob(store.Job{
		ID: "running", Status: store.StatusRunning, CreatedAt: time.Now().UTC(),
		Stats: store.Stats{Bytes: 1024, TotalBytes: 2048, Speed: 1024},
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/widget", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	text, _ := body["text"].(string)
	if !strings.Contains(text, "1,0 КБ из 2,0 КБ") || !strings.Contains(text, "1,0 КБ/с") {
		t.Fatalf("text = %q", text)
	}
	if strings.ContainsRune(text, 'Ð') || strings.ContainsRune(text, '‘') {
		t.Fatalf("text contains byte-sliced units: %q", text)
	}
}

func TestPreflightUsesMaxTransferThreshold(t *testing.T) {
	root := t.TempDir()
	_, server, engine := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	free, err := disk.Free(root)
	if err != nil || free == 0 {
		t.Fatalf("free space: %d, %v", free, err)
	}
	preflight := func(bytes int64) bool {
		t.Helper()
		engine.setResponse("operations/size", map[string]any{"count": 1, "bytes": bytes})
		resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/preflight", map[string]any{
			"accountId": accountID, "sourcePath": "Фото", "destPath": root,
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
		}
		fits, _ := body["fits"].(bool)
		return fits
	}

	// 80% of the free space fits even with the 10% safety margin.
	if !preflight(int64(disk.TransferLimit(free) * 8 / 10)) {
		t.Fatal("a transfer well inside the limit must fit")
	}
	// 95% of the raw free space is more than rclone's 90% limit: the old code
	// said "fits" because it compared against 100%.
	if preflight(int64(free - free/20)) {
		t.Fatal("a transfer above the 90% max-transfer limit must not fit")
	}
}

func TestResumeRejectsInvalidStoredDest(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	if err := st.AddJob(store.Job{
		ID: "paused", AccountID: accountID, Source: "cf_x:",
		Dest: "/etc/outside", Status: store.StatusInterrupted, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add job: %v", err)
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/paused/resume", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); message == "" {
		t.Fatalf("empty error: %v", body)
	}
	if got := engine.count("sync/copy"); got != 0 {
		t.Fatalf("sync/copy calls = %d, want 0", got)
	}
	stored, _ := st.Job("paused")
	if stored.Status != store.StatusInterrupted {
		t.Fatalf("status = %q, want interrupted", stored.Status)
	}
}
