package yandex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func withEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	previous := Endpoint
	Endpoint = endpoint
	t.Cleanup(func() { Endpoint = previous })
}

func TestAuthURL(t *testing.T) {
	withEndpoint(t, "https://example.test")

	got := AuthURL("client-123")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Path != "/authorize" {
		t.Fatalf("path = %q", parsed.Path)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client-123" {
		t.Fatalf("client_id = %q", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != VerificationRedirect {
		t.Fatalf("redirect_uri = %q", query.Get("redirect_uri"))
	}
	if query.Get("response_type") != "code" {
		t.Fatalf("response_type = %q", query.Get("response_type"))
	}
}

func TestExchange(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotForm = r.PostForm
		_, _ = w.Write([]byte(`{"access_token":"at","refresh_token":"rt","expires_in":3600,"token_type":"bearer"}`))
	}))
	defer server.Close()
	withEndpoint(t, server.URL)

	token, err := Exchange(context.Background(), "cid", "secret", " 1234567 ")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if token.AccessToken != "at" || token.RefreshToken != "rt" {
		t.Fatalf("token = %+v", token)
	}
	if gotForm.Get("grant_type") != "authorization_code" || gotForm.Get("code") != "1234567" {
		t.Fatalf("form = %v", gotForm)
	}
	if gotForm.Get("client_secret") != "secret" {
		t.Fatalf("client_secret missing: %v", gotForm)
	}
}

func TestExchangeOmitsEmptySecret(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotForm = r.PostForm
		_, _ = w.Write([]byte(`{"access_token":"at"}`))
	}))
	defer server.Close()
	withEndpoint(t, server.URL)

	if _, err := Exchange(context.Background(), "cid", "", "1234567"); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if _, ok := gotForm["client_secret"]; ok {
		t.Fatalf("client_secret must be omitted, form = %v", gotForm)
	}
}

func TestExchangeErrorDescription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Неверный код подтверждения"}`))
	}))
	defer server.Close()
	withEndpoint(t, server.URL)

	_, err := Exchange(context.Background(), "cid", "secret", "0000000")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Неверный код подтверждения") {
		t.Fatalf("error = %v", err)
	}
}

func TestExchangeRequiresCode(t *testing.T) {
	withEndpoint(t, "https://example.test")
	if _, err := Exchange(context.Background(), "cid", "", "  "); err == nil {
		t.Fatal("expected error")
	}
}

func TestRcloneTokenShape(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	encoded, err := Token{AccessToken: "at", RefreshToken: "rt", ExpiresIn: 3600}.RcloneToken(now)
	if err != nil {
		t.Fatalf("rclone token: %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["access_token"] != "at" || decoded["refresh_token"] != "rt" {
		t.Fatalf("decoded = %v", decoded)
	}
	if decoded["token_type"] != "OAuth" {
		t.Fatalf("token_type = %q", decoded["token_type"])
	}
	if decoded["expiry"] != "2026-11-13T13:00:00Z" {
		t.Fatalf("expiry = %q", decoded["expiry"])
	}
}
