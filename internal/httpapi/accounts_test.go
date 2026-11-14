package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"cloudferry/internal/store"
)

func createWebDAVAccount(t *testing.T, serverURL string) map[string]any {
	t.Helper()
	client := http.DefaultClient
	resp, body := doJSON(t, client, http.MethodPost, serverURL+"/api/accounts", map[string]any{
		"name":     "Яндекс Диск",
		"provider": "webdav",
		"url":      "https://webdav.yandex.ru",
		"user":     "user@yandex.ru",
		"password": "app-password",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create webdav: status = %d, body = %v", resp.StatusCode, body)
	}
	return body
}

func TestCreateWebDAVAccountStoresLogin(t *testing.T) {
	server, st, _ := newTestServer(t)
	body := createWebDAVAccount(t, server.URL)
	if body["name"] != "user@yandex.ru" || body["login"] != "user@yandex.ru" {
		t.Fatalf("name/login = %v/%v, want the WebDAV login", body["name"], body["login"])
	}

	accounts := st.Accounts()
	if len(accounts) != 1 || accounts[0].Login != "user@yandex.ru" || accounts[0].Name != "user@yandex.ru" {
		t.Fatalf("stored accounts = %+v", accounts)
	}

	_, state := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/state", nil)
	list, _ := state["accounts"].([]any)
	if len(list) != 1 {
		t.Fatalf("state accounts = %v", state["accounts"])
	}
	account, _ := list[0].(map[string]any)
	if account["name"] != "user@yandex.ru" || account["login"] != "user@yandex.ru" {
		t.Fatalf("state account = %v", account)
	}
}

func TestTokenAccountKeepsNameWithoutLogin(t *testing.T) {
	server, st, _ := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Моё облако", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["name"] != "Моё облако" {
		t.Fatalf("name = %v", body["name"])
	}
	if _, exists := body["login"]; exists {
		t.Fatalf("token account must not invent a login: %v", body)
	}
	if accounts := st.Accounts(); accounts[0].Login != "" {
		t.Fatalf("stored login = %q", accounts[0].Login)
	}

	_, state := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/state", nil)
	list, _ := state["accounts"].([]any)
	account, _ := list[0].(map[string]any)
	if _, exists := account["login"]; exists {
		t.Fatalf("state account must omit login: %v", account)
	}
}

// An account saved before the login field existed must keep working: state
// still renders it and the missing login is simply absent.
func TestLegacyAccountWithoutLoginStillWorks(t *testing.T) {
	server, st, _ := newTestServer(t)
	legacy := store.Account{
		ID: "legacy", Name: "Яндекс Диск", Provider: "yandex-token", Remote: "cf_legacy",
	}
	if err := st.AddAccount(legacy); err != nil {
		t.Fatalf("add legacy account: %v", err)
	}

	_, state := doJSON(t, server.Client(), http.MethodGet, server.URL+"/api/state", nil)
	list, _ := state["accounts"].([]any)
	if len(list) != 1 {
		t.Fatalf("state accounts = %v", state["accounts"])
	}
	account, _ := list[0].(map[string]any)
	if account["name"] != "Яндекс Диск" {
		t.Fatalf("name = %v", account["name"])
	}
	if _, exists := account["login"]; exists {
		t.Fatalf("legacy account must not appear as a known login: %v", account)
	}
}

func TestProbeReportsKnownWebDAVUser(t *testing.T) {
	server, _, _ := newTestServer(t)
	created := createWebDAVAccount(t, server.URL)
	accountID, _ := created["id"].(string)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts/"+accountID+"/probe", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["knownUser"] != true || body["login"] != "user@yandex.ru" {
		t.Fatalf("knownUser/login = %v/%v", body["knownUser"], body["login"])
	}
}

func TestProbeReadsLoginFromRcloneConfig(t *testing.T) {
	server, st, engine := newTestServer(t)
	legacy := store.Account{
		ID: "legacy", Name: "Яндекс Диск", Provider: "webdav", Remote: "cf_legacy",
	}
	if err := st.AddAccount(legacy); err != nil {
		t.Fatalf("add legacy account: %v", err)
	}
	engine.setResponse("config/get", map[string]any{
		"type": "webdav", "url": "https://webdav.yandex.ru", "user": "legacy@yandex.ru",
	})

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts/legacy/probe", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["knownUser"] != true || body["login"] != "legacy@yandex.ru" {
		t.Fatalf("knownUser/login = %v/%v", body["knownUser"], body["login"])
	}
	call, ok := engine.find("config/get")
	if !ok || call.input["name"] != "cf_legacy" {
		t.Fatalf("config/get call = %+v", call)
	}
}

func TestProbeUnknownUserIsNotGuessed(t *testing.T) {
	server, st, engine := newTestServer(t)
	_, created := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name": "Яндекс Диск", "provider": "yandex-token", "token": `{"access_token":"x"}`,
	})
	accountID, _ := created["id"].(string)

	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts/"+accountID+"/probe", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["knownUser"] != false || body["login"] != "" {
		t.Fatalf("knownUser/login = %v/%v, want unknown", body["knownUser"], body["login"])
	}

	// A failing config lookup is not an error: the probe still answers about
	// the disk and reports the user as unknown.
	legacy := store.Account{ID: "legacy", Name: "Яндекс Диск", Provider: "webdav", Remote: "cf_legacy"}
	if err := st.AddAccount(legacy); err != nil {
		t.Fatalf("add legacy: %v", err)
	}
	engine.setError("config/get", errors.New("rclone is down"))
	resp, body = doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts/legacy/probe", nil)
	if resp.StatusCode != http.StatusOK || body["knownUser"] != false {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestCreateWebDAVAccountTrimsLogin(t *testing.T) {
	server, st, engine := newTestServer(t)
	resp, body := doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/accounts", map[string]any{
		"name":     "Яндекс Диск",
		"provider": "webdav",
		"url":      "https://webdav.yandex.ru",
		"user":     "  user@yandex.ru  ",
		"password": "app-password",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["login"] != "user@yandex.ru" || body["name"] != "user@yandex.ru" {
		t.Fatalf("name/login = %v/%v", body["name"], body["login"])
	}
	if accounts := st.Accounts(); accounts[0].Login != "user@yandex.ru" {
		t.Fatalf("stored login = %q", accounts[0].Login)
	}
	call, ok := engine.find("config/create")
	if !ok {
		t.Fatal("config/create was not called")
	}
	params, _ := call.input["parameters"].(map[string]any)
	if params["user"] != "user@yandex.ru" {
		t.Fatalf("rclone user = %v", params["user"])
	}
}
