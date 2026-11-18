package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cloudferry/internal/jobs"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

func webdavValidatePayload(password string) map[string]any {
	return map[string]any{
		"name":     "Моё облако",
		"provider": "webdav",
		"url":      "https://webdav.yandex.ru",
		"user":     "user@yandex.ru",
		"password": password,
	}
}

func tokenValidatePayload(token string) map[string]any {
	return map[string]any{
		"name":     "Моё облако",
		"provider": "yandex-token",
		"token":    token,
	}
}

func doValidate(t *testing.T, server *httptest.Server, payload map[string]any) (*http.Response, map[string]any) {
	t.Helper()
	return doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts/validate", payload)
}

// engineCalls returns every recorded call of one method, in order.
func engineCalls(engine *fakeEngine, method string) []recordedCall {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	var out []recordedCall
	for _, call := range engine.calls {
		if call.method == method {
			out = append(out, call)
		}
	}
	return out
}

func singleCall(t *testing.T, engine *fakeEngine, method string) recordedCall {
	t.Helper()
	calls := engineCalls(engine, method)
	if len(calls) != 1 {
		t.Fatalf("%s calls = %d, want 1", method, len(calls))
	}
	return calls[0]
}

func createdRemoteName(t *testing.T, engine *fakeEngine) string {
	t.Helper()
	call := singleCall(t, engine, "config/create")
	name, _ := call.input["name"].(string)
	if !strings.HasPrefix(name, "cfcheck_") || len(name) == len("cfcheck_") {
		t.Fatalf("temporary remote name = %q, want cfcheck_<random>", name)
	}
	return name
}

// storeSnapshot renders the persisted state so a test can prove it did not
// change during a validation request.
func storeSnapshot(st *store.Store) string {
	accounts, jobs := st.Snapshot()
	return fmt.Sprintf("%#v|%#v", accounts, jobs)
}

func assertStoreUntouched(t *testing.T, before string, st *store.Store, server *httptest.Server) {
	t.Helper()
	if after := storeSnapshot(st); after != before {
		t.Fatalf("store changed by validation:\nbefore %s\nafter  %s", before, after)
	}
	_, state := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/state", nil)
	if accounts, _ := state["accounts"].([]any); len(accounts) != 0 {
		t.Fatalf("state accounts = %v, want none", state["accounts"])
	}
	if jobList, _ := state["jobs"].([]any); len(jobList) != 0 {
		t.Fatalf("state jobs = %v, want none", state["jobs"])
	}
}

func TestValidateWebDAVAccountSucceedsWithoutCreatingAccount(t *testing.T) {
	server, st, engine := newTestServer(t)
	before := storeSnapshot(st)

	payload := webdavValidatePayload("app-password")
	payload["user"] = "  user@yandex.ru  "
	resp, body := doValidate(t, server, payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["ok"] != true || body["login"] != "user@yandex.ru" {
		t.Fatalf("ok/login = %v/%v", body["ok"], body["login"])
	}
	about, _ := body["about"].(map[string]any)
	if about["total"] != float64(1000) || about["used"] != float64(400) || about["free"] != float64(600) {
		t.Fatalf("about = %v", body["about"])
	}

	name := createdRemoteName(t, engine)
	create := singleCall(t, engine, "config/create")
	if create.input["type"] != "webdav" {
		t.Fatalf("type = %v", create.input["type"])
	}
	params, _ := create.input["parameters"].(map[string]any)
	if params["url"] != "https://webdav.yandex.ru" || params["vendor"] != "other" ||
		params["user"] != "user@yandex.ru" || params["pass"] != "app-password" {
		t.Fatalf("parameters = %v", params)
	}
	opt, _ := create.input["opt"].(map[string]any)
	if opt["obscure"] != true || opt["nonInteractive"] != true {
		t.Fatalf("opt = %v", opt)
	}

	aboutCall := singleCall(t, engine, "operations/about")
	if aboutCall.input["fs"] != name+":" {
		t.Fatalf("about fs = %v, want %q", aboutCall.input["fs"], name+":")
	}
	listCall := singleCall(t, engine, "operations/list")
	if listCall.input["fs"] != name+":" || listCall.input["remote"] != "" {
		t.Fatalf("list input = %v", listCall.input)
	}
	if opt, _ := listCall.input["opt"].(map[string]any); opt["dirsOnly"] != true {
		t.Fatalf("list opt = %v", listCall.input["opt"])
	}

	deleteCall := singleCall(t, engine, "config/delete")
	if deleteCall.input["name"] != name {
		t.Fatalf("deleted remote = %v, want %q", deleteCall.input["name"], name)
	}

	assertStoreUntouched(t, before, st, server)
}

func TestValidateUsesFreshTemporaryRemotePerCheck(t *testing.T) {
	server, _, engine := newTestServer(t)

	if resp, body := doValidate(t, server, webdavValidatePayload("app-password")); resp.StatusCode != http.StatusOK {
		t.Fatalf("first check: %d %v", resp.StatusCode, body)
	}
	if resp, body := doValidate(t, server, webdavValidatePayload("app-password")); resp.StatusCode != http.StatusOK {
		t.Fatalf("second check: %d %v", resp.StatusCode, body)
	}

	names := map[string]bool{}
	for _, call := range engineCalls(engine, "config/create") {
		name, _ := call.input["name"].(string)
		if !strings.HasPrefix(name, "cfcheck_") {
			t.Fatalf("remote name = %q", name)
		}
		if names[name] {
			t.Fatalf("the same temporary remote name was used twice: %q", name)
		}
		names[name] = true
	}
	if len(names) != 2 {
		t.Fatalf("config/create calls = %d, want 2", len(names))
	}
	if deletes := engineCalls(engine, "config/delete"); len(deletes) != 2 {
		t.Fatalf("config/delete calls = %d, want 2", len(deletes))
	}
}

func TestValidateYandexTokenAccountSucceeds(t *testing.T) {
	server, st, engine := newTestServer(t)
	before := storeSnapshot(st)

	resp, body := doValidate(t, server, tokenValidatePayload(`{"access_token":"at"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["ok"] != true || body["login"] != "" {
		t.Fatalf("ok/login = %v/%v, want an empty login for a token", body["ok"], body["login"])
	}
	name := createdRemoteName(t, engine)
	create := singleCall(t, engine, "config/create")
	if create.input["type"] != "yandex" {
		t.Fatalf("type = %v", create.input["type"])
	}
	params, _ := create.input["parameters"].(map[string]any)
	if params["token"] != `{"access_token":"at"}` {
		t.Fatalf("parameters = %v", params)
	}
	if about := singleCall(t, engine, "operations/about"); about.input["fs"] != name+":" {
		t.Fatalf("about fs = %v", about.input["fs"])
	}
	if deleted := singleCall(t, engine, "config/delete"); deleted.input["name"] != name {
		t.Fatalf("deleted = %v, want %q", deleted.input["name"], name)
	}
	assertStoreUntouched(t, before, st, server)
}

func TestValidateAuthFailureWebDAV401(t *testing.T) {
	server, st, engine := newTestServer(t)
	before := storeSnapshot(st)
	engine.setError("operations/about", errors.New("401 Unauthorized"))

	resp, body := doValidate(t, server, webdavValidatePayload("app-password"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["code"] != "auth_failed" {
		t.Fatalf("code = %v", body["code"])
	}
	if _, exists := body["retryAfterHint"]; exists {
		t.Fatalf("401 must not claim the password is still activating: %v", body)
	}
	if body["field"] != "password" {
		t.Fatalf("field = %v, want password", body["field"])
	}
	message, _ := body["error"].(string)
	if message == "" || strings.Contains(message, "401") || strings.Contains(message, "rclone") {
		t.Fatalf("error must be a clean user sentence, got %q", message)
	}

	name := createdRemoteName(t, engine)
	if deleted := singleCall(t, engine, "config/delete"); deleted.input["name"] != name {
		t.Fatalf("temporary remote %q was not deleted: %v", name, deleted.input)
	}
	assertStoreUntouched(t, before, st, server)
}

func TestValidateAuthFailureWebDAV403HasNoActivationHint(t *testing.T) {
	server, _, engine := newTestServer(t)
	engine.setError("operations/about", errors.New("403 Forbidden"))

	resp, body := doValidate(t, server, webdavValidatePayload("app-password"))
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "auth_failed" {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if _, exists := body["retryAfterHint"]; exists {
		t.Fatalf("403 must not promise a retry window: %v", body)
	}
	if len(engineCalls(engine, "config/delete")) != 1 {
		t.Fatalf("temporary remote was not deleted")
	}
}

func TestValidateAuthFailureYandexToken(t *testing.T) {
	server, _, engine := newTestServer(t)
	engine.setError("operations/about", errors.New("401 Unauthorized"))

	resp, body := doValidate(t, server, tokenValidatePayload(`{"access_token":"bad"}`))
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "auth_failed" {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["field"] != "token" {
		t.Fatalf("field = %v, want token", body["field"])
	}
	if _, exists := body["retryAfterHint"]; exists {
		t.Fatalf("token auth failure must not carry a WebDAV hint: %v", body)
	}
	message, _ := body["error"].(string)
	if !strings.Contains(message, "токен") {
		t.Fatalf("error = %q, want a token message", message)
	}
	if len(engineCalls(engine, "config/delete")) != 1 {
		t.Fatalf("temporary remote was not deleted")
	}
}

// rclone's yandex backend reports a revoked token as invalid_grant without an
// HTTP 401; that is still "wrong credentials", not an outage.
func TestValidateAuthFailureYandexTokenInvalidGrant(t *testing.T) {
	engine := &fakeEngine{responses: map[string]any{
		"operations/about": map[string]any{"total": 1},
	}}
	engine.setError("operations/about", errors.New(`oauth2: "invalid_grant" "token has been revoked"`))
	server, _ := newValidateTestServer(t, engine)

	resp, body := doValidate(t, server, tokenValidatePayload(`{"access_token":"revoked"}`))
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "auth_failed" {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestValidateWebDAVUnavailable(t *testing.T) {
	server, _, engine := newTestServer(t)
	engine.setError("operations/about", errors.New("405 Method Not Allowed"))

	resp, body := doValidate(t, server, webdavValidatePayload("app-password"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["code"] != "webdav_unavailable" {
		t.Fatalf("code = %v, want webdav_unavailable", body["code"])
	}
	if _, exists := body["retryAfterHint"]; exists {
		t.Fatalf("unavailable WebDAV is not an activation hint: %v", body)
	}
	if len(engineCalls(engine, "config/delete")) != 1 {
		t.Fatalf("temporary remote was not deleted")
	}
}

// A disabled WebDAV may answer 403 with a body explaining why; that is not a
// wrong password and must not show the activation hint.
func TestValidateWebDAVDisabledIsNotAuthFailure(t *testing.T) {
	engine := &fakeEngine{responses: map[string]any{
		"operations/about": map[string]any{"total": 1},
	}}
	engine.setError("operations/about", &rc.APIError{
		Method: "operations/about",
		Status: http.StatusForbidden,
		Body:   "WebDAV is disabled for this account",
	})
	server, _ := newValidateTestServer(t, engine)

	resp, body := doValidate(t, server, webdavValidatePayload("app-password"))
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "webdav_unavailable" {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestValidateTimeout(t *testing.T) {
	previous := validateTimeout
	validateTimeout = 100 * time.Millisecond
	t.Cleanup(func() { validateTimeout = previous })

	inner := &fakeEngine{responses: map[string]any{
		"operations/about": map[string]any{"total": 1},
	}}
	engine := &blockingEngine{fakeEngine: inner, method: "operations/about"}
	server, _ := newValidateTestServer(t, engine)

	start := time.Now()
	resp, body := doValidate(t, server, webdavValidatePayload("app-password"))
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["code"] != "timeout" {
		t.Fatalf("code = %v, want timeout", body["code"])
	}
	if elapsed > 3*time.Second {
		t.Fatalf("validation ignored its timeout: %v", elapsed)
	}
	if len(engineCalls(inner, "config/delete")) != 1 {
		t.Fatalf("temporary remote was not deleted after a timeout")
	}
}

func TestValidateRcloneUnavailableOnCreateIs502(t *testing.T) {
	server, st, engine := newTestServer(t)
	before := storeSnapshot(st)
	engine.setError("config/create", errors.New("dial unix /run/rclone/rclone.sock: connect: connection refused"))

	resp, body := doValidate(t, server, webdavValidatePayload("app-password"))
	if resp.StatusCode != http.StatusBadGateway || body["code"] != "upstream_error" {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if len(engineCalls(engine, "config/delete")) != 0 {
		t.Fatalf("nothing was created, so nothing must be deleted")
	}
	assertStoreUntouched(t, before, st, server)
}

func TestValidateUpstreamErrorDeletesTemporaryRemote(t *testing.T) {
	server, _, engine := newTestServer(t)
	engine.setError("operations/list", errors.New("connection reset by peer"))

	resp, body := doValidate(t, server, webdavValidatePayload("app-password"))
	if resp.StatusCode != http.StatusBadGateway || body["code"] != "upstream_error" {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	name := createdRemoteName(t, engine)
	deleted := singleCall(t, engine, "config/delete")
	if deleted.input["name"] != name {
		t.Fatalf("deleted remote = %v, want %q", deleted.input["name"], name)
	}
}

func TestValidateInputValidation(t *testing.T) {
	server, _, engine := newTestServer(t)

	longURL := "https://webdav.yandex.ru/" + strings.Repeat("a", maxValidateURLLen)
	cases := []struct {
		name   string
		body   map[string]any
		field  string
		status int
	}{
		{"empty body", map[string]any{}, "provider", http.StatusBadRequest},
		{"unknown provider", map[string]any{"provider": "ftp"}, "provider", http.StatusBadRequest},
		{"missing url", map[string]any{"provider": "webdav", "user": "u", "password": "p"}, "url", http.StatusBadRequest},
		{"missing user", map[string]any{"provider": "webdav", "url": "https://webdav.yandex.ru", "password": "p"}, "user", http.StatusBadRequest},
		{"missing password", map[string]any{"provider": "webdav", "url": "https://webdav.yandex.ru", "user": "u"}, "password", http.StatusBadRequest},
		{"relative url", map[string]any{"provider": "webdav", "url": "webdav.yandex.ru", "user": "u", "password": "p"}, "url", http.StatusBadRequest},
		{"url too long", map[string]any{"provider": "webdav", "url": longURL, "user": "u", "password": "p"}, "url", http.StatusBadRequest},
		{"user too long", map[string]any{"provider": "webdav", "url": "https://webdav.yandex.ru", "user": strings.Repeat("u", maxValidateUserLen+1), "password": "p"}, "user", http.StatusBadRequest},
		{"password too long", map[string]any{"provider": "webdav", "url": "https://webdav.yandex.ru", "user": "u", "password": strings.Repeat("p", maxValidatePasswordLen+1)}, "password", http.StatusBadRequest},
		{"missing token", map[string]any{"provider": "yandex-token"}, "token", http.StatusBadRequest},
		{"malformed token", map[string]any{"provider": "yandex-token", "token": "not-json"}, "token", http.StatusBadRequest},
		{"token without access_token", map[string]any{"provider": "yandex-token", "token": `{"x":1}`}, "token", http.StatusBadRequest},
		{"token too long", map[string]any{"provider": "yandex-token", "token": strings.Repeat("a", maxValidateTokenLen+1)}, "token", http.StatusBadRequest},
		{"client id too long", map[string]any{"provider": "yandex-token", "token": `{"access_token":"at"}`, "clientId": strings.Repeat("c", maxValidateUserLen+1)}, "clientId", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doValidate(t, server, tc.body)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
			}
			if body["code"] != "invalid_request" {
				t.Fatalf("code = %v", body["code"])
			}
			if body["field"] != tc.field {
				t.Fatalf("field = %v, want %q", body["field"], tc.field)
			}
			if message, _ := body["error"].(string); message == "" {
				t.Fatalf("empty error: %v", body)
			}
		})
	}
	if called := len(engineCalls(engine, "config/create")); called != 0 {
		t.Fatalf("invalid input reached rclone: %d config/create calls", called)
	}
}

func TestValidateRejectsClientSuppliedRemoteName(t *testing.T) {
	server, _, engine := newTestServer(t)
	payload := webdavValidatePayload("app-password")
	payload["remote"] = "cf_evil"

	resp, body := doValidate(t, server, payload)
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "invalid_request" {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if len(engineCalls(engine, "config/create")) != 0 {
		t.Fatal("a client-supplied remote name must not reach rclone")
	}
}

func TestValidateRejectsGet(t *testing.T) {
	server, _, _ := newTestServer(t)
	resp, err := server.Client().Get(server.URL + "/api/accounts/validate")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

// The handler must never write a password or token into the log, even if the
// upstream error text contains one.
func TestValidateDoesNotLogSecrets(t *testing.T) {
	var buf bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	secret := "S3cret-App-Password"
	server, _, engine := newTestServer(t)
	engine.setError("operations/about", fmt.Errorf("401 Unauthorized for password %s", secret))

	resp, body := doValidate(t, server, webdavValidatePayload(secret))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if strings.Contains(buf.String(), secret) {
		t.Fatalf("password leaked into the log: %s", buf.String())
	}
	if message, _ := body["error"].(string); strings.Contains(message, secret) {
		t.Fatalf("password leaked into the response: %q", message)
	}
}

func TestValidateDoesNotLogToken(t *testing.T) {
	var buf bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	token := `{"access_token":"tok-secret-123"}`
	server, _, engine := newTestServer(t)
	engine.setError("operations/about", fmt.Errorf("401 invalid token %s", token))

	resp, body := doValidate(t, server, tokenValidatePayload(token))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if strings.Contains(buf.String(), "tok-secret-123") {
		t.Fatalf("token leaked into the log: %s", buf.String())
	}
	if message, _ := body["error"].(string); strings.Contains(message, "tok-secret-123") {
		t.Fatalf("token leaked into the response: %q", message)
	}
}

// blockingEngine answers one method only when the caller's deadline expires.
type blockingEngine struct {
	*fakeEngine
	method string
}

func (b *blockingEngine) Call(ctx context.Context, method string, in, out any) error {
	if method == b.method {
		<-ctx.Done()
		return ctx.Err()
	}
	return b.fakeEngine.Call(ctx, method, in, out)
}

// newValidateTestServer builds a server around any Engine implementation.
func newValidateTestServer(t *testing.T, engine Engine) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	api, err := New(Config{
		Store:     st,
		Rclone:    engine,
		Jobs:      jobs.NewManager(st, engine, jobs.Options{}),
		Downloads: "/downloads",
		Version:   "test",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return server, st
}

func TestIsLocalHostRejectsZoneAndNumericForms(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"fe80::1%eth0", true},
		{"fe80::1", true},
		{"fd00::1", true},
		{"2130706433", true},
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"example.com", false},
		{"8.8.8.8", false},
		{"2a02:6b8::2:242", false},
	}
	for _, c := range cases {
		if got := isLocalHost(c.host); got != c.want {
			t.Errorf("isLocalHost(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestCreateAccountRejectsZoneLinkLocal(t *testing.T) {
	for _, raw := range []string{
		"http://[fe80::1%25eth0]/dav",
		"http://[fe80::1%eth0]/dav",
		"http://2130706433/dav",
	} {
		t.Run(raw, func(t *testing.T) {
			server, _, _ := newTestServer(t)
			body := map[string]any{
				"provider": "webdav", "url": raw, "user": "u", "password": "p",
			}
			resp, data := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, data)
			}
			if data["field"] != "url" {
				t.Fatalf("field = %v, want url, body = %v", data["field"], data)
			}
		})
	}
}

func TestWebDAVHostAllowlist(t *testing.T) {
	cases := []struct {
		name string
		host string
		env  string
		want bool
	}{
		{"yandex default", "webdav.yandex.ru", "", true},
		{"yandex uppercase", "WebDAV.Yandex.RU", "", true},
		{"trailing dot", "webdav.yandex.ru.", "", true},
		{"foreign host", "evil.example.com", "", false},
		{"nip.io to loopback", "127.0.0.1.nip.io", "", false},
		{"ip address", "8.8.8.8", "", false},
		{"env override", "dav.example.com", "dav.example.com", true},
		{"env override keeps default", "webdav.yandex.ru", "dav.example.com", true},
		{"env with spaces", "dav.example.com", " dav.example.com , other ", true},
		{"empty env entry", "dav.example.com", ",,", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("CF_WEBDAV_HOSTS", c.env)
			if got := allowedWebDAVHost(c.host); got != c.want {
				t.Fatalf("allowedWebDAVHost(%q) = %v, want %v", c.host, got, c.want)
			}
		})
	}
}

func TestCreateAccountRejectsForeignWebDAVHost(t *testing.T) {
	server, _, _ := newTestServer(t)
	body := map[string]any{
		"provider": "webdav", "url": "https://evil.example.com/dav", "user": "u", "password": "p",
	}
	resp, data := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %v", resp.StatusCode, data)
	}
	if data["field"] != "url" {
		t.Fatalf("field = %v, want url", data["field"])
	}
}
