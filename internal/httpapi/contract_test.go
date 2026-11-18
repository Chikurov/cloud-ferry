package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

// Two simultaneous downloads into one folder with different client keys must
// produce exactly one job: the key lookup cannot replay across different keys,
// so only the destination claim can keep the folder single-writer. The outcome
// is order-independent: whoever records first wins, the other gets 409.
func TestConcurrentCreateJobDifferentKeysOneConflict(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)

	type result struct {
		status int
		body   map[string]any
		err    error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"race-key-one", "race-key-two"} {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, _, body, err := postCreateJobRaw(server.Client(), server.URL, map[string]any{
				"accountId": accountID, "destPath": "/downloads/race", "clientToken": key,
			}, nil)
			results <- result{status: status, body: body, err: err}
		}()
	}
	wg.Wait()
	close(results)

	created, conflicted := 0, 0
	for res := range results {
		if res.err != nil {
			t.Fatalf("create job: %v", res.err)
		}
		switch res.status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicted++
			if message, _ := res.body["error"].(string); message == "" {
				t.Fatalf("conflict answer without a message: %v", res.body)
			}
		default:
			t.Fatalf("unexpected status %d: %v", res.status, res.body)
		}
	}
	if created != 1 || conflicted != 1 {
		t.Fatalf("created = %d, conflict = %d, want one job and one conflict", created, conflicted)
	}

	jobs := st.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want one record", len(jobs))
	}
	if jobs[0].Dest != "/downloads/race" {
		t.Fatalf("dest = %q", jobs[0].Dest)
	}
	if jobs[0].IdempotencyKey == "" {
		t.Fatalf("created job kept no idempotency key: %+v", jobs[0])
	}
	if got := engine.count("sync/copy"); got != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", got)
	}
}

// A folder that disappeared or turned into a file is a user mistake: preflight
// answers 400 with the field to fix, never 502 from the sizing call. The
// upstream-404 shape and a partially missing folder list must map the same way.
func TestPreflightSourceFolderErrorsAreBadRequests(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*fakeEngine)
		body    func(accountID string) map[string]any
		field   string
		missing string
	}{
		{
			name: "rclone reports the folder missing",
			setup: func(engine *fakeEngine) {
				engine.setError("operations/stat", &rc.APIError{
					Method: "operations/stat", Status: http.StatusNotFound, Body: "not found",
				})
			},
			body: func(accountID string) map[string]any {
				return map[string]any{"accountId": accountID, "sourcePath": "Пропавшая", "destPath": "/downloads/x"}
			},
			field:   "sourcePath",
			missing: "Пропавшая",
		},
		{
			name: "selected path is a file",
			setup: func(engine *fakeEngine) {
				engine.setStat("Файл.txt", map[string]any{"IsDir": false})
			},
			body: func(accountID string) map[string]any {
				return map[string]any{"accountId": accountID, "sourcePath": "Файл.txt", "destPath": "/downloads/x"}
			},
			field:   "sourcePath",
			missing: "Файл.txt",
		},
		{
			name: "one folder of a list is missing",
			setup: func(engine *fakeEngine) {
				engine.setStat("Есть", map[string]any{"IsDir": true})
				engine.setStat("Пропавшая", nil)
			},
			body: func(accountID string) map[string]any {
				return map[string]any{
					"accountId": accountID, "sourcePaths": []string{"Есть", "Пропавшая"}, "destPath": "/downloads/x",
				}
			},
			field:   "sourcePaths",
			missing: "Пропавшая",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _, engine := newTestServer(t)
			accountID := createTestAccount(t, server)
			tc.setup(engine)

			resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/preflight", tc.body(accountID))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (a missing folder is not an outage), body = %v", resp.StatusCode, body)
			}
			if body["field"] != tc.field {
				t.Fatalf("field = %v, want %q, body = %v", body["field"], tc.field, body)
			}
			message, _ := body["error"].(string)
			if !strings.Contains(message, tc.missing) {
				t.Fatalf("error = %q, want it to name %q", message, tc.missing)
			}
			if got := engine.count("operations/size"); got != 0 {
				t.Fatalf("operations/size calls = %d, want 0 for an invalid source", got)
			}
		})
	}
}

// POST /api/accounts must reject the same local hosts as the validate endpoint
// without calling rclone, accept a public address once, and enforce every form
// length boundary instead of letting an oversized value reach the engine.
func TestCreateAccountRejectsLocalHostsAndEnforcesLengths(t *testing.T) {
	webdav := func(url, user, password string) map[string]any {
		return map[string]any{"provider": "webdav", "url": url, "user": user, "password": password}
	}
	const validURL = "https://webdav.yandex.ru"

	t.Run("rejected local hosts", func(t *testing.T) {
		cases := []struct {
			name string
			url  string
		}{
			{"10/8 private network", "http://10.1.2.3"},
			{"172.16/12 private network", "http://172.20.0.1"},
			{"unspecified address", "http://0.0.0.0"},
			{"ipv6 unique local", "http://[fc00::1]"},
			{"ipv6 link local", "http://[fe80::1]"},
			{"localhost subdomain", "http://foo.localhost"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				server, st, engine := newTestServer(t)
				resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", webdav(tc.url, "u", "p"))
				if resp.StatusCode != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, body)
				}
				if body["field"] != "url" {
					t.Fatalf("field = %v, want url, body = %v", body["field"], body)
				}
				message, _ := body["error"].(string)
				if !strings.Contains(message, "публичный адрес") {
					t.Fatalf("error = %q, want the public-address explanation", message)
				}
				if got := engine.count("config/create"); got != 0 {
					t.Fatalf("config/create calls = %d, want 0 for a local host", got)
				}
				if accounts := st.Accounts(); len(accounts) != 0 {
					t.Fatalf("a rejected host stored accounts: %+v", accounts)
				}
			})
		}
	})

	t.Run("accepted public hosts", func(t *testing.T) {
		cases := []struct {
			name string
			url  string
		}{
			{"public ipv4 literal", "https://93.184.216.34"},
			{"public ipv6 literal", "https://[2606:2800:220:1:248:1893:25c8:1946]"},
			{"public hostname", validURL},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				server, st, engine := newTestServer(t)
				resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", webdav(tc.url, "u", "p"))
				if resp.StatusCode != http.StatusCreated {
					t.Fatalf("status = %d, want 201, body = %v", resp.StatusCode, body)
				}
				if got := engine.count("config/create"); got != 1 {
					t.Fatalf("config/create calls = %d, want 1", got)
				}
				if accounts := st.Accounts(); len(accounts) != 1 {
					t.Fatalf("accounts = %+v, want one", accounts)
				}
			})
		}
	})

	t.Run("length boundaries", func(t *testing.T) {
		urlPrefix := "https://webdav.yandex.ru/"
		maxURL := urlPrefix + strings.Repeat("a", maxValidateURLLen-len(urlPrefix))
		longURL := urlPrefix + strings.Repeat("a", maxValidateURLLen-len(urlPrefix)+1)
		cases := []struct {
			name     string
			body     map[string]any
			want     int
			field    string
			contains string
		}{
			{"url at the limit", webdav(maxURL, "u", "p"), http.StatusCreated, "", ""},
			{"url one over the limit", webdav(longURL, "u", "p"), http.StatusBadRequest, "url", "слишком длинный"},
			{"user at the limit", webdav(validURL, strings.Repeat("u", maxValidateUserLen), "p"), http.StatusCreated, "", ""},
			{"user one over the limit", webdav(validURL, strings.Repeat("u", maxValidateUserLen+1), "p"), http.StatusBadRequest, "user", "слишком длинный"},
			{"password at the limit", webdav(validURL, "u", strings.Repeat("p", maxValidatePasswordLen)), http.StatusCreated, "", ""},
			{"password one over the limit", webdav(validURL, "u", strings.Repeat("p", maxValidatePasswordLen+1)), http.StatusBadRequest, "password", "слишком длинный"},
			{"token at the limit",
				map[string]any{"provider": "yandex-token", "token": strings.Repeat("a", maxValidateTokenLen)},
				http.StatusCreated, "", ""},
			{"token one over the limit",
				map[string]any{"provider": "yandex-token", "token": strings.Repeat("a", maxValidateTokenLen+1)},
				http.StatusBadRequest, "token", "слишком длинный"},
			{"client id at the limit",
				map[string]any{"provider": "yandex-token", "token": `{"access_token":"x"}`, "clientId": strings.Repeat("c", maxValidateUserLen)},
				http.StatusCreated, "", ""},
			{"client id one over the limit",
				map[string]any{"provider": "yandex-token", "token": `{"access_token":"x"}`, "clientId": strings.Repeat("c", maxValidateUserLen+1)},
				http.StatusBadRequest, "clientId", "слишком длинный"},
			{"client secret at the limit",
				map[string]any{"provider": "yandex-token", "token": `{"access_token":"x"}`, "clientSecret": strings.Repeat("s", maxValidateUserLen)},
				http.StatusCreated, "", ""},
			{"client secret one over the limit",
				map[string]any{"provider": "yandex-token", "token": `{"access_token":"x"}`, "clientSecret": strings.Repeat("s", maxValidateUserLen+1)},
				http.StatusBadRequest, "clientSecret", "слишком длинный"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				server, st, engine := newTestServer(t)
				resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", tc.body)
				if resp.StatusCode != tc.want {
					t.Fatalf("status = %d, want %d, body = %v", resp.StatusCode, tc.want, body)
				}
				if tc.want == http.StatusCreated {
					if got := engine.count("config/create"); got != 1 {
						t.Fatalf("config/create calls = %d, want 1", got)
					}
					if accounts := st.Accounts(); len(accounts) != 1 {
						t.Fatalf("accounts = %+v, want one", accounts)
					}
					return
				}
				if body["field"] != tc.field {
					t.Fatalf("field = %v, want %q, body = %v", body["field"], tc.field, body)
				}
				message, _ := body["error"].(string)
				if !strings.Contains(message, tc.contains) {
					t.Fatalf("error = %q, want it to contain %q", message, tc.contains)
				}
				if got := engine.count("config/create"); got != 0 {
					t.Fatalf("config/create calls = %d, want 0 for an oversized value", got)
				}
				if accounts := st.Accounts(); len(accounts) != 0 {
					t.Fatalf("an oversized value stored accounts: %+v", accounts)
				}
			})
		}
	})
}

// The clientToken field must accept exactly the same key alphabet and lengths as
// the Idempotency-Key header: 8 to 128 latin characters, digits and . _ : -.
func TestCreateJobClientTokenKeyBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		want    int
		message string
	}{
		{"shortest", "abcdefgh", http.StatusCreated, ""},
		{"longest", strings.Repeat("b", maxIdempotencyKeyLen), http.StatusCreated, ""},
		{"all allowed separators", "Ab0._:-x", http.StatusCreated, ""},
		{"one short", "abcdefg", http.StatusBadRequest, ""},
		{"one long", strings.Repeat("b", maxIdempotencyKeyLen+1), http.StatusBadRequest, ""},
		{"space", "abc defgh", http.StatusBadRequest, ""},
		{"plus", "abcdefg+", http.StatusBadRequest, ""},
		{"slash", "abc/defgh", http.StatusBadRequest, ""},
		{"at sign", "abc@defgh", http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, st, engine := newTestServer(t)
			accountID := createTestAccount(t, server)
			body := map[string]any{
				"accountId": accountID, "destPath": "/downloads/token-boundary", "clientToken": tc.key,
			}

			resp, answer := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d, body = %v", resp.StatusCode, tc.want, answer)
			}
			if tc.want == http.StatusCreated {
				resp, replay := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs", body)
				if resp.StatusCode != http.StatusOK || resp.Header.Get("Idempotent-Replay") != "true" {
					t.Fatalf("replay status = %d, header = %q, body = %v",
						resp.StatusCode, resp.Header.Get("Idempotent-Replay"), replay)
				}
				if replay["id"] != answer["id"] {
					t.Fatalf("replay returned job %v, want %v", replay["id"], answer["id"])
				}
				if jobs := st.Jobs(); len(jobs) != 1 {
					t.Fatalf("jobs = %d, want 1", len(jobs))
				}
				if calls := engine.count("sync/copy"); calls != 1 {
					t.Fatalf("sync/copy calls = %d, want 1", calls)
				}
				return
			}

			if answer["code"] != "invalid_request" || answer["field"] != "clientToken" {
				t.Fatalf("answer = %v, want invalid_request on clientToken", answer)
			}
			if jobs := st.Jobs(); len(jobs) != 0 {
				t.Fatalf("an invalid token created %d jobs", len(jobs))
			}
			if calls := engine.count("sync/copy"); calls != 0 {
				t.Fatalf("sync/copy calls = %d, want 0", calls)
			}
		})
	}
}

// The header has priority even when the body token is unusable: the body field
// must not fail a request that carries a valid header key. Pinning this keeps
// the two key sources from ever being validated together.
func TestCreateJobValidHeaderWinsOverInvalidClientToken(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	body := map[string]any{
		"accountId": accountID, "destPath": "/downloads/priority", "clientToken": "bad key 1",
	}

	status, header, first := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("header-key-1"))
	if status != http.StatusCreated || header.Get("Idempotent-Replay") != "" {
		t.Fatalf("status = %d, header = %q, body = %v", status, header.Get("Idempotent-Replay"), first)
	}

	body["clientToken"] = "bad key 2"
	status, header, second := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("header-key-1"))
	if status != http.StatusOK || header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay status = %d, header = %q, body = %v", status, header.Get("Idempotent-Replay"), second)
	}
	if second["id"] != first["id"] {
		t.Fatalf("replay returned job %v, want %v", second["id"], first["id"])
	}
	if jobs := st.Jobs(); len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if calls := engine.count("sync/copy"); calls != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", calls)
	}
}

// A 409 from a reused key must name the field the client has to fix, so the UI
// can point at the header or at the body token instead of guessing.
func TestCreateJobConflictNamesKeyField(t *testing.T) {
	cases := []struct {
		name   string
		header bool
		first  string
		second string
		field  string
	}{
		{"header key", true, "/downloads/header-a", "/downloads/header-b", "Idempotency-Key"},
		{"client token", false, "/downloads/token-a", "/downloads/token-b", "clientToken"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, st, engine := newTestServer(t)
			accountID := createTestAccount(t, server)
			request := func(dest string) (map[string]any, map[string]string) {
				body := map[string]any{"accountId": accountID, "destPath": dest}
				if tc.header {
					return body, idempotencyHeader("conflict-key-1")
				}
				body["clientToken"] = "conflict-token-1"
				return body, nil
			}

			first, headers := request(tc.first)
			if status, _, body := postCreateJob(t, server.Client(), server.URL, first, headers); status != http.StatusCreated {
				t.Fatalf("first status = %d, body = %v", status, body)
			}
			second, headers := request(tc.second)
			status, _, body := postCreateJob(t, server.Client(), server.URL, second, headers)
			if status != http.StatusConflict {
				t.Fatalf("status = %d, want 409, body = %v", status, body)
			}
			if body["code"] != "idempotency_conflict" {
				t.Fatalf("code = %v, body = %v", body["code"], body)
			}
			if body["field"] != tc.field {
				t.Fatalf("field = %v, want %q, body = %v", body["field"], tc.field, body)
			}
			if jobs := st.Jobs(); len(jobs) != 1 {
				t.Fatalf("jobs = %d, want 1", len(jobs))
			}
			if calls := engine.count("sync/copy"); calls != 1 {
				t.Fatalf("sync/copy calls = %d, want 1", calls)
			}
		})
	}
}

// A key must not stay stuck on a failed launch: once the engine is back, a
// retry with the same key starts a fresh download instead of replaying 502.
func TestFailedLaunchRetrySucceedsAfterEngineRecovers(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	engine.setError("sync/copy", errors.New("engine refused the transfer"))
	body := map[string]any{
		"accountId": accountID, "sourcePath": "Фото", "destPath": "/downloads/retry", "mode": "copy",
	}

	for attempt := 1; attempt <= 2; attempt++ {
		status, header, answer := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("recover-key-1"))
		if status != http.StatusBadGateway {
			t.Fatalf("attempt %d status = %d, want 502, body = %v", attempt, status, answer)
		}
		if header.Get("Idempotent-Replay") != "" {
			t.Fatalf("attempt %d was answered as a replay: %q", attempt, header.Get("Idempotent-Replay"))
		}
	}

	// The engine is reachable again: the same key is a new attempt now.
	engine.setError("sync/copy", nil)
	status, header, created := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("recover-key-1"))
	if status != http.StatusCreated {
		t.Fatalf("recovered attempt status = %d, want 201, body = %v", status, created)
	}
	if header.Get("Idempotent-Replay") != "" {
		t.Fatalf("recovered attempt answered as a replay: %q", header.Get("Idempotent-Replay"))
	}
	if created["status"] != store.StatusRunning {
		t.Fatalf("created status = %v, want running", created["status"])
	}
	if created["idempotencyKey"] != "recover-key-1" {
		t.Fatalf("created key = %v, want recover-key-1", created["idempotencyKey"])
	}
	if calls := engine.count("sync/copy"); calls != 3 {
		t.Fatalf("sync/copy calls = %d, want 3", calls)
	}

	failed := 0
	for _, job := range st.Jobs() {
		if job.Status == store.StatusFailed {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("failed jobs = %d, want two failed attempts", failed)
	}
}
