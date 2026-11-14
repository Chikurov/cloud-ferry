package rc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func fakeServer(t *testing.T, handler http.HandlerFunc) (socket string) {
	t.Helper()

	// macOS caps unix socket paths at ~104 bytes, so use a short directory
	// instead of t.TempDir() with the test name in it.
	dir, err := os.MkdirTemp("/tmp", "cf")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket = filepath.Join(dir, "r.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = os.Remove(socket)
	})
	return socket
}

func TestCallSendsJSONAndAuth(t *testing.T) {
	var gotPath, gotUser, gotPass, gotBody string
	socket := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotUser, gotPass, _ = r.BasicAuth()
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"v1.76.0","decomposed":[1,76,0],"isGit":false}`))
	})

	client := NewClient(socket, "user", "secret")
	var version Version
	if err := client.Call(context.Background(), "core/version", map[string]any{"short": true}, &version); err != nil {
		t.Fatalf("call: %v", err)
	}

	if gotPath != "/core/version" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotUser != "user" || gotPass != "secret" {
		t.Fatalf("auth = %q/%q", gotUser, gotPass)
	}
	if gotBody != `{"short":true}` {
		t.Fatalf("body = %q", gotBody)
	}
	if version.Version != "v1.76.0" {
		t.Fatalf("version = %q", version.Version)
	}
}

func TestCallSurfacesErrorFieldWith200(t *testing.T) {
	socket := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":"[410 - GoneError] Gone"}`))
	})

	client := NewClient(socket, "", "")
	err := client.Call(context.Background(), "operations/list", map[string]any{"fs": "yandex:"}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.Body != "[410 - GoneError] Gone" {
		t.Fatalf("body = %q", apiErr.Body)
	}
}

func TestCallSurfacesNon200(t *testing.T) {
	socket := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream"}`))
	})

	client := NewClient(socket, "", "")
	err := client.Call(context.Background(), "core/stats", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
		t.Fatalf("expected 502 APIError, got %v", err)
	}
}

func TestCallRejectsMethodsOutsideAllowlist(t *testing.T) {
	var hits atomic.Int32
	socket := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	})

	client := NewClient(socket, "", "")
	// The allowlist is deliberately small: methods the app does not use are
	// removed from it, starting with the ones that never had a caller.
	for _, method := range []string{
		"core/command", "config/dump", "config/setpath", "operations/getfile", "mount/mount",
		"job/list", "operations/fsinfo", "config/update", "config/listremotes", "config/providers",
	} {
		err := client.Call(context.Background(), method, nil, nil)
		if !errors.Is(err, ErrMethodNotAllowed) {
			t.Fatalf("%s: expected ErrMethodNotAllowed, got %v", method, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("server was hit %d times", hits.Load())
	}
}

func TestPing(t *testing.T) {
	socket := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"v1.76.0"}`))
	})
	client := NewClient(socket, "", "")
	version, err := client.Ping(context.Background())
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if version.Version != "v1.76.0" {
		t.Fatalf("version = %q", version.Version)
	}
}

func TestUnreachableSocketIsNotAPIError(t *testing.T) {
	client := NewClient(filepath.Join(t.TempDir(), "missing.sock"), "", "")
	err := client.Call(context.Background(), "core/version", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsUnavailable(err) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestCallReportsOversizedResponse(t *testing.T) {
	previous := maxResponseBytes
	maxResponseBytes = 64
	t.Cleanup(func() { maxResponseBytes = previous })

	socket := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"padding":"` + strings.Repeat("x", 200) + `"}`))
	})

	client := NewClient(socket, "", "")
	var out map[string]any
	err := client.Call(context.Background(), "operations/list", nil, &out)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("expected ErrResponseTooLarge, got %v", err)
	}
}
