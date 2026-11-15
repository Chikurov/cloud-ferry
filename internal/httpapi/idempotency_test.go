package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// postCreateJobRaw posts one job and reports what a test observes. It takes no
// *testing.T so a goroutine can use it: only the test goroutine may fail a test.
func postCreateJobRaw(client *http.Client, url string, body map[string]any, headers map[string]string) (int, http.Header, map[string]any, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url+"/api/jobs", strings.NewReader(string(payload)))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return resp.StatusCode, resp.Header, nil, err
	}
	return resp.StatusCode, resp.Header, decoded, nil
}

// postCreateJob is postCreateJobRaw for the test goroutine.
func postCreateJob(t *testing.T, client *http.Client, url string, body map[string]any, headers map[string]string) (int, http.Header, map[string]any) {
	t.Helper()
	status, header, decoded, err := postCreateJobRaw(client, url, body, headers)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	return status, header, decoded
}

func idempotencyHeader(key string) map[string]string {
	return map[string]string{"Idempotency-Key": key}
}

func TestCreateJobReplaysSameKey(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	body := map[string]any{
		"accountId": accountID, "sourcePath": "Фото", "destPath": "/downloads/idem", "mode": "copy",
	}

	status, header, first := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("retry-key-1"))
	if status != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", status, first)
	}
	if header.Get("Idempotent-Replay") != "" {
		t.Fatalf("a new job must not be marked as a replay: %q", header.Get("Idempotent-Replay"))
	}

	status, header, second := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("retry-key-1"))
	if status != http.StatusOK {
		t.Fatalf("replay status = %d, body = %v", status, second)
	}
	if header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("Idempotent-Replay = %q, want true", header.Get("Idempotent-Replay"))
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

func TestCreateJobClientTokenReplays(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	body := map[string]any{
		"accountId": accountID, "destPath": "/downloads/token", "clientToken": "client-token-1",
	}

	status, _, first := postCreateJob(t, server.Client(), server.URL, body, nil)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", status, first)
	}
	status, header, second := postCreateJob(t, server.Client(), server.URL, body, nil)
	if status != http.StatusOK {
		t.Fatalf("replay status = %d, body = %v", status, second)
	}
	if header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("Idempotent-Replay = %q", header.Get("Idempotent-Replay"))
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

// TestCreateJobHeaderWinsOverClientToken pins the priority rule: when both
// sources carry a key, only the header is used, so the body token neither
// replays nor conflicts.
func TestCreateJobHeaderWinsOverClientToken(t *testing.T) {
	server, st, _ := newTestServer(t)
	accountID := createTestAccount(t, server)
	body := map[string]any{
		"accountId": accountID, "destPath": "/downloads/priority", "clientToken": "body-token-1",
	}

	status, _, first := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("header-key-1"))
	if status != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", status, first)
	}

	// The header repeats the key while the body token differs: the header wins,
	// so this is a replay of the first job.
	otherToken := map[string]any{
		"accountId": accountID, "destPath": "/downloads/priority", "clientToken": "body-token-2",
	}
	status, header, second := postCreateJob(t, server.Client(), server.URL, otherToken, idempotencyHeader("header-key-1"))
	if status != http.StatusOK || header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay status = %d, header = %q, body = %v", status, header.Get("Idempotent-Replay"), second)
	}
	if second["id"] != first["id"] {
		t.Fatalf("replay returned job %v, want %v", second["id"], first["id"])
	}

	// The body token alone is another key, so it starts a new job elsewhere.
	third := map[string]any{
		"accountId": accountID, "destPath": "/downloads/priority-2", "clientToken": "body-token-1",
	}
	status, header, created := postCreateJob(t, server.Client(), server.URL, third, nil)
	if status != http.StatusCreated || header.Get("Idempotent-Replay") != "" {
		t.Fatalf("status = %d, header = %q, body = %v", status, header.Get("Idempotent-Replay"), created)
	}
	if created["id"] == first["id"] {
		t.Fatal("a body-only key must not reuse the job created under the header key")
	}
	if jobs := st.Jobs(); len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
}

func TestCreateJobDifferentKeyCreatesNewJob(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)

	status, _, first := postCreateJob(t, server.Client(), server.URL, map[string]any{
		"accountId": accountID, "destPath": "/downloads/one",
	}, idempotencyHeader("key-first-1"))
	if status != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", status, first)
	}
	status, header, second := postCreateJob(t, server.Client(), server.URL, map[string]any{
		"accountId": accountID, "destPath": "/downloads/two",
	}, idempotencyHeader("key-second-2"))
	if status != http.StatusCreated || header.Get("Idempotent-Replay") != "" {
		t.Fatalf("status = %d, header = %q, body = %v", status, header.Get("Idempotent-Replay"), second)
	}
	if second["id"] == first["id"] {
		t.Fatal("a different key must create a different job")
	}
	if jobs := st.Jobs(); len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
	if calls := engine.count("sync/copy"); calls != 2 {
		t.Fatalf("sync/copy calls = %d, want 2", calls)
	}
}

// TestCreateJobSameKeyOtherParametersConflict makes sure one key names one
// operation: the same key with another destination, source or mode is refused
// instead of answering with the wrong job.
func TestCreateJobSameKeyOtherParametersConflict(t *testing.T) {
	cases := []struct {
		name   string
		second map[string]any
	}{
		{"other destination", map[string]any{"destPath": "/downloads/other"}},
		{"other source", map[string]any{"sourcePath": "Документы"}},
		{"other mode", map[string]any{"mode": "sync"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, st, engine := newTestServer(t)
			accountID := createTestAccount(t, server)
			first := map[string]any{
				"accountId": accountID, "sourcePath": "Фото", "destPath": "/downloads/idem", "mode": "copy",
			}
			status, _, body := postCreateJob(t, server.Client(), server.URL, first, idempotencyHeader("same-key-1"))
			if status != http.StatusCreated {
				t.Fatalf("first status = %d, body = %v", status, body)
			}

			second := map[string]any{"accountId": accountID, "sourcePath": "Фото", "destPath": "/downloads/idem", "mode": "copy"}
			for name, value := range tc.second {
				second[name] = value
			}
			status, _, body = postCreateJob(t, server.Client(), server.URL, second, idempotencyHeader("same-key-1"))
			if status != http.StatusConflict {
				t.Fatalf("status = %d, want 409, body = %v", status, body)
			}
			if body["code"] != "idempotency_conflict" {
				t.Fatalf("code = %v, body = %v", body["code"], body)
			}
			if jobs := st.Jobs(); len(jobs) != 1 {
				t.Fatalf("jobs = %d, want 1", len(jobs))
			}
			if calls := engine.count("sync/copy") + engine.count("sync/sync"); calls != 1 {
				t.Fatalf("transfer calls = %d, want 1", calls)
			}
		})
	}
}

func TestCreateJobInvalidKeyRejected(t *testing.T) {
	cases := []struct {
		name      string
		header    string
		token     string
		wantField string
	}{
		{"too short in header", "abc1234", "", "Idempotency-Key"},
		{"too long in header", strings.Repeat("a", 129), "", "Idempotency-Key"},
		{"space in header", "bad key 1", "", "Idempotency-Key"},
		{"slash in header", "key/with/slash", "", "Idempotency-Key"},
		{"cyrillic token", "", "ключик123", "clientToken"},
		{"too short token", "", "abc", "clientToken"},
		{"invalid header wins over valid token", "bad key 1", "good-token-1", "Idempotency-Key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, st, _ := newTestServer(t)
			accountID := createTestAccount(t, server)
			headers := map[string]string{}
			if tc.header != "" {
				headers["Idempotency-Key"] = tc.header
			}
			body := map[string]any{"accountId": accountID, "destPath": "/downloads/bad-key"}
			if tc.token != "" {
				body["clientToken"] = tc.token
			}
			status, _, answer := postCreateJob(t, server.Client(), server.URL, body, headers)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %v", status, answer)
			}
			if answer["code"] != "invalid_request" {
				t.Fatalf("code = %v, body = %v", answer["code"], answer)
			}
			if answer["field"] != tc.wantField {
				t.Fatalf("field = %v, want %q, body = %v", answer["field"], tc.wantField, answer)
			}
			if jobs := st.Jobs(); len(jobs) != 0 {
				t.Fatalf("an invalid key must not create a job: %d", len(jobs))
			}
		})
	}
}

// TestCreateJobKeyBoundaries pins the accepted length and alphabet edges.
func TestCreateJobKeyBoundaries(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"shortest", "abcdefgh"},
		{"longest", strings.Repeat("b", 128)},
		{"all allowed separators", "Ab0._:-x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, st, _ := newTestServer(t)
			accountID := createTestAccount(t, server)
			body := map[string]any{"accountId": accountID, "destPath": "/downloads/boundary"}
			status, _, answer := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader(tc.key))
			if status != http.StatusCreated {
				t.Fatalf("status = %d, body = %v", status, answer)
			}
			status, header, _ := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader(tc.key))
			if status != http.StatusOK || header.Get("Idempotent-Replay") != "true" {
				t.Fatalf("replay status = %d, header = %q", status, header.Get("Idempotent-Replay"))
			}
			if jobs := st.Jobs(); len(jobs) != 1 {
				t.Fatalf("jobs = %d, want 1", len(jobs))
			}
		})
	}
}

// TestCreateJobWithoutKeyKeepsBehaviour checks that a request without a key is
// not deduplicated: only the existing destination rule applies.
func TestCreateJobWithoutKeyKeepsBehaviour(t *testing.T) {
	server, st, _ := newTestServer(t)
	accountID := createTestAccount(t, server)
	body := map[string]any{"accountId": accountID, "destPath": "/downloads/plain"}

	status, header, first := postCreateJob(t, server.Client(), server.URL, body, nil)
	if status != http.StatusCreated || header.Get("Idempotent-Replay") != "" {
		t.Fatalf("status = %d, header = %q, body = %v", status, header.Get("Idempotent-Replay"), first)
	}
	status, header, second := postCreateJob(t, server.Client(), server.URL, body, nil)
	if status != http.StatusConflict || header.Get("Idempotent-Replay") != "" {
		t.Fatalf("status = %d, header = %q, body = %v", status, header.Get("Idempotent-Replay"), second)
	}

	status, _, third := postCreateJob(t, server.Client(), server.URL, map[string]any{
		"accountId": accountID, "destPath": "/downloads/plain-2",
	}, nil)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", status, third)
	}
	if jobs := st.Jobs(); len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
}

// TestCreateJobReplaySkipsSourceCheck keeps a retry useful when the provider is
// briefly unreachable: the answer comes from the stored job.
func TestCreateJobReplaySkipsSourceCheck(t *testing.T) {
	server, _, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	body := map[string]any{"accountId": accountID, "sourcePath": "Фото", "destPath": "/downloads/offline"}

	status, _, answer := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("offline-key-1"))
	if status != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", status, answer)
	}

	engine.setError("operations/stat", errors.New("provider is down"))
	status, header, replayed := postCreateJob(t, server.Client(), server.URL, body, idempotencyHeader("offline-key-1"))
	if status != http.StatusOK || header.Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay status = %d, header = %q, body = %v", status, header.Get("Idempotent-Replay"), replayed)
	}
	if replayed["id"] != answer["id"] {
		t.Fatalf("replay returned job %v, want %v", replayed["id"], answer["id"])
	}
}

// TestConcurrentCreateJobSameKeyOneJob covers the race the mutex must prevent:
// simultaneous retries of one request start one transfer.
func TestConcurrentCreateJobSameKeyOneJob(t *testing.T) {
	server, st, engine := newTestServer(t)
	accountID := createTestAccount(t, server)
	body := map[string]any{
		"accountId": accountID, "sourcePath": "Фото", "destPath": "/downloads/race", "clientToken": "race-key-1",
	}

	const requests = 4
	type result struct {
		status int
		replay string
		id     string
		err    error
	}
	results := make(chan result, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, header, decoded, err := postCreateJobRaw(server.Client(), server.URL, body, nil)
			id, _ := decoded["id"].(string)
			results <- result{status: status, replay: header.Get("Idempotent-Replay"), id: id, err: err}
		}()
	}
	wg.Wait()
	close(results)

	created, replayed := 0, 0
	ids := map[string]struct{}{}
	for res := range results {
		if res.err != nil {
			t.Fatalf("create job: %v", res.err)
		}
		switch res.status {
		case http.StatusCreated:
			created++
		case http.StatusOK:
			replayed++
			if res.replay != "true" {
				t.Fatalf("a 200 answer must carry Idempotent-Replay: %+v", res)
			}
		default:
			t.Fatalf("unexpected status %d", res.status)
		}
		if res.id == "" {
			t.Fatalf("answer without a job id: %+v", res)
		}
		ids[res.id] = struct{}{}
	}
	if created != 1 || replayed != requests-1 {
		t.Fatalf("created = %d, replayed = %d", created, replayed)
	}
	if len(ids) != 1 {
		t.Fatalf("job ids = %v, want one", ids)
	}
	if jobs := st.Jobs(); len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if calls := engine.count("sync/copy"); calls != 1 {
		t.Fatalf("sync/copy calls = %d, want 1", calls)
	}
}
