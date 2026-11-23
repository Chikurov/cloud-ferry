package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"cloudferry/internal/store"
)

// A failed launch must not turn the retry into a 200 replay: the second POST
// with the same key attempts the transfer again and fails again.
func TestReviewFailedLaunchIsRetriedWithSameKey(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setError("sync/copy", errors.New("engine refused the transfer"))
	body := map[string]any{
		"accountId": accountID, "sourcePath": "Фото", "destPath": "/downloads/retry", "mode": "copy",
	}

	status, header, first := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("retry-key-1"))
	if status != http.StatusBadGateway {
		t.Fatalf("first status = %d, want 502, body = %v", status, first)
	}
	if header.Get("Idempotent-Replay") != "" {
		t.Fatalf("a failed launch must not be marked as a replay: %q", header.Get("Idempotent-Replay"))
	}

	status, header, second := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("retry-key-1"))
	if status != http.StatusBadGateway {
		t.Fatalf("retry status = %d, want a second 502, body = %v", status, second)
	}
	if header.Get("Idempotent-Replay") != "" {
		t.Fatalf("retry answered as a replay: %q", header.Get("Idempotent-Replay"))
	}
	if calls := engine.count("sync/copy"); calls != 2 {
		t.Fatalf("sync/copy calls = %d, want 2", calls)
	}
	jobs := st.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want two failed attempts", len(jobs))
	}
	for _, job := range jobs {
		if job.Status != store.StatusFailed || job.IdempotencyKey != "" {
			t.Fatalf("stored job = %+v, want failed without a replayable key", job)
		}
	}
}

// POST /api/accounts must run the same form validation as
// POST /api/accounts/validate, including the local-network ban.
func TestReviewCreateAccountValidatesInput(t *testing.T) {
	cases := []struct {
		name    string
		body    map[string]any
		field   string
		message string
	}{
		{
			"missing url",
			map[string]any{"provider": "webdav", "user": "u", "password": "p"},
			"url", "",
		},
		{
			"missing user",
			map[string]any{"provider": "webdav", "url": "https://webdav.yandex.ru", "password": "p"},
			"user", "",
		},
		{
			"missing password",
			map[string]any{"provider": "webdav", "url": "https://webdav.yandex.ru", "user": "u"},
			"password", "",
		},
		{
			"not an http url",
			map[string]any{"provider": "webdav", "url": "ftp://webdav.yandex.ru", "user": "u", "password": "p"},
			"url", "",
		},
		{
			"loopback ip",
			map[string]any{"provider": "webdav", "url": "http://127.0.0.1:8080", "user": "u", "password": "p"},
			"url", "публичный адрес",
		},
		{
			"private ip",
			map[string]any{"provider": "webdav", "url": "http://192.168.1.10", "user": "u", "password": "p"},
			"url", "публичный адрес",
		},
		{
			"link-local ip",
			map[string]any{"provider": "webdav", "url": "http://169.254.10.1", "user": "u", "password": "p"},
			"url", "публичный адрес",
		},
		{
			"ipv6 loopback",
			map[string]any{"provider": "webdav", "url": "http://[::1]:8080", "user": "u", "password": "p"},
			"url", "публичный адрес",
		},
		{
			"localhost name",
			map[string]any{"provider": "webdav", "url": "http://localhost:8080", "user": "u", "password": "p"},
			"url", "публичный адрес",
		},
		{
			"long user",
			map[string]any{
				"provider": "webdav", "url": "https://webdav.yandex.ru",
				"user": strings.Repeat("u", maxValidateUserLen+1), "password": "p",
			},
			"user", "",
		},
		{
			"unknown provider",
			map[string]any{"provider": "ftp"},
			"provider", "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, st, engine := newTestServer(t)
			resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, body)
			}
			if body["field"] != tc.field {
				t.Fatalf("field = %v, want %q, body = %v", body["field"], tc.field, body)
			}
			if tc.message != "" {
				message, _ := body["error"].(string)
				if !strings.Contains(message, tc.message) {
					t.Fatalf("error = %q, want it to contain %q", message, tc.message)
				}
			}
			if calls := engine.count("config/create"); calls != 0 {
				t.Fatalf("config/create calls = %d, want 0 for an invalid form", calls)
			}
			if accounts := st.Accounts(); len(accounts) != 0 {
				t.Fatalf("an invalid form stored accounts: %+v", accounts)
			}
		})
	}
}

// A folder that disappeared is a user mistake: preflight answers 400 with the
// field to fix, not 502 from the sizing call.
func TestReviewPreflightMissingFolderIsBadRequest(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setResponse("operations/stat", map[string]any{"item": nil})

	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{
			"legacy single folder",
			map[string]any{"accountId": accountID, "sourcePath": "Пропавшая", "destPath": "/downloads/x"},
			"sourcePath",
		},
		{
			"folder list",
			map[string]any{"accountId": accountID, "sourcePaths": []string{"Пропавшая"}, "destPath": "/downloads/y"},
			"sourcePaths",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/preflight", tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, body)
			}
			if body["field"] != tc.field {
				t.Fatalf("field = %v, want %q, body = %v", body["field"], tc.field, body)
			}
			message, _ := body["error"].(string)
			if !strings.Contains(message, "Пропавшая") {
				t.Fatalf("error = %q, want the missing folder named", message)
			}
		})
	}
	if calls := engine.count("operations/size"); calls != 0 {
		t.Fatalf("operations/size calls = %d, want 0 for a missing folder", calls)
	}
}

// The liveness probe is public: it must not echo rclone's socket or host
// details back to the caller.
func TestReviewHealthzHidesRcloneError(t *testing.T) {
	server, _, engine := newTestServer(t)
	engine.setPingError(errors.New("dial unix /run/rclone/rclone.sock: connect: connection refused"))

	resp, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if body["ok"] != false {
		t.Fatalf("body = %v, want ok=false", body)
	}
	message, _ := body["error"].(string)
	if message != "rclone недоступен" {
		t.Fatalf("error = %q, want the short user phrase", message)
	}
	if strings.Contains(message, "unix") || strings.Contains(message, ".sock") || strings.Contains(message, "dial") {
		t.Fatalf("healthz leaked rclone internals: %q", message)
	}
}

// Check is allowed only for a finished attempt, and a second click answers 409
// instead of repeating the expensive comparison.
func TestReviewCheckRejectsRunningAndRepeatedCheck(t *testing.T) {
	server, st, engine := newTestServer(t)
	if err := st.AddJob(store.Job{
		ID: "running", Status: store.StatusRunning, Source: "cf_x:",
		Dest: "/downloads/a", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add running job: %v", err)
	}

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/running/check", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("check while running: status = %d, want 409, body = %v", resp.StatusCode, body)
	}
	if calls := engine.count("operations/check"); calls != 0 {
		t.Fatalf("operations/check calls = %d, want 0 for a running job", calls)
	}

	if err := st.AddJob(store.Job{
		ID: "done", Status: store.StatusDone, Source: "cf_x:",
		Dest: "/downloads/b", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add done job: %v", err)
	}
	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/done/check", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first check: status = %d, body = %v", resp.StatusCode, body)
	}
	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/done/check", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second check: status = %d, want 409, body = %v", resp.StatusCode, body)
	}
	if calls := engine.count("operations/check"); calls != 1 {
		t.Fatalf("operations/check calls = %d, want 1", calls)
	}
}

func TestWholeDiskExclusionsReachEstimateAndTransfer(t *testing.T) {
	server, _, engine := newTestServer(t)
	body := map[string]any{
		"accountId": createTestAccount(t, server), "destPath": "/downloads/filtered",
		"excludedFolders": []string{"Архив", "Архив/2025"},
	}
	for _, tc := range []struct {
		path   string
		method string
		status int
	}{
		{"/api/preflight", "operations/size", http.StatusOK},
		{"/api/jobs", "sync/copy", http.StatusCreated},
	} {
		resp, data := doJSON(t, server.Client(), http.MethodPost, server.URL+tc.path, body)
		if resp.StatusCode != tc.status {
			t.Fatalf("%s: %d %v", tc.path, resp.StatusCode, data)
		}
		call, ok := engine.find(tc.method)
		if !ok {
			t.Fatalf("missing %s", tc.method)
		}
		filter, _ := call.input["_filter"].(map[string]any)
		rules, _ := filter["ExcludeRule"].([]string)
		if len(rules) != 1 || rules[0] != "/Архив/**" {
			t.Fatalf("%s exclusion rules = %#v", tc.method, filter)
		}
	}
	body["sourceFiles"] = []string{"заметки.txt"}
	resp, data := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/preflight", body)
	if resp.StatusCode != http.StatusBadRequest || data["field"] != "excludedFolders" {
		t.Fatalf("mixed source = %d %v", resp.StatusCode, data)
	}
}
