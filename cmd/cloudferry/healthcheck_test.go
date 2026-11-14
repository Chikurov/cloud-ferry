package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRunHealthcheckOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != healthcheckPath {
			t.Errorf("path = %q, want %q", r.URL.Path, healthcheckPath)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if code := runHealthcheck(server.URL+healthcheckPath, "", ""); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestRunHealthcheckBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "umbrel" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	target := server.URL + healthcheckPath
	if code := runHealthcheck(target, "", ""); code != 1 {
		t.Fatalf("exit code without credentials = %d, want 1", code)
	}
	if code := runHealthcheck(target, "umbrel", "secret"); code != 0 {
		t.Fatalf("exit code with credentials = %d, want 0", code)
	}
}

func TestRunHealthcheckServerDown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	target := server.URL + healthcheckPath
	server.Close()

	if code := runHealthcheck(target, "", ""); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestHealthcheckTargetFromListenAddr(t *testing.T) {
	tests := []struct {
		listen string
		want   string
	}{
		{"0.0.0.0:3000", "http://127.0.0.1:3000/api/healthz"},
		{":8741", "http://127.0.0.1:8741/api/healthz"},
		{"127.0.0.1:3000", "http://127.0.0.1:3000/api/healthz"},
		{"[::]:3000", "http://127.0.0.1:3000/api/healthz"},
		{"not-an-address", "http://127.0.0.1:3000/api/healthz"},
	}
	for _, tt := range tests {
		if got := healthcheckTarget(tt.listen); got != tt.want {
			t.Errorf("healthcheckTarget(%q) = %q, want %q", tt.listen, got, tt.want)
		}
	}
}
