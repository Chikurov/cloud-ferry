package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func postCreateDir(t *testing.T, serverURL string, client *http.Client, body any) (*http.Response, map[string]any) {
	t.Helper()
	return doJSON(t, client, http.MethodPost, serverURL+"/api/fs/dirs", body)
}

func TestFSCreateDir(t *testing.T) {
	root := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)
	created := filepath.Join(root, "фото")

	resp, body := postCreateDir(t, server.URL, server.Client(), map[string]any{"path": created})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["path"] != created {
		t.Fatalf("path = %v, want %q", body["path"], created)
	}
	info, err := os.Stat(created)
	if err != nil || !info.IsDir() {
		t.Fatalf("created dir: info=%v err=%v", info, err)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "фото" {
		t.Fatalf("root entries = %v", entries)
	}
}

func TestFSCreateDirKeepsExistingParentOnly(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "one")
	mkdirAll(t, parent)
	created := filepath.Join(parent, "two")
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := postCreateDir(t, server.URL, server.Client(), map[string]any{"path": created})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["path"] != created {
		t.Fatalf("path = %v", body["path"])
	}
}

func TestFSCreateDirDuplicate(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "фото")
	mkdirAll(t, existing)
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := postCreateDir(t, server.URL, server.Client(), map[string]any{"path": existing})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "Папка уже существует") {
		t.Fatalf("error = %q", message)
	}

	// The allowed root itself is not created twice either.
	resp, body = postCreateDir(t, server.URL, server.Client(), map[string]any{"path": root})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("root: status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestFSCreateDirOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)

	for _, target := range []string{
		filepath.Join(outside, "фото"),
		root + "/../outside",
		"/etc/cloud-ferry",
	} {
		resp, body := postCreateDir(t, server.URL, server.Client(), map[string]any{"path": target})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%q: status = %d, body = %v", target, resp.StatusCode, body)
		}
		if message, _ := body["error"].(string); message == "" {
			t.Fatalf("%q: empty error", target)
		}
	}
}

func TestFSCreateDirRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	symlink(t, outside, filepath.Join(root, "escape"))
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := postCreateDir(t, server.URL, server.Client(), map[string]any{
		"path": filepath.Join(root, "escape", "child"),
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "ведёт за пределы") {
		t.Fatalf("error = %q", message)
	}
	if _, err := os.Stat(filepath.Join(outside, "child")); !os.IsNotExist(err) {
		t.Fatalf("created outside the root: %v", err)
	}
}

func TestFSCreateDirRelativePath(t *testing.T) {
	root := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)

	for _, target := range []string{"downloads/фото", "", "   "} {
		resp, body := postCreateDir(t, server.URL, server.Client(), map[string]any{"path": target})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%q: status = %d, body = %v", target, resp.StatusCode, body)
		}
		if message, _ := body["error"].(string); !strings.Contains(message, "Укажите полный путь") {
			t.Fatalf("%q: error = %q", target, message)
		}
	}
}

func TestFSCreateDirRequiresExistingParent(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := postCreateDir(t, server.URL, server.Client(), map[string]any{
		"path": filepath.Join(missing, "child"),
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "Папка не найдена") {
		t.Fatalf("error = %q", message)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("parent was created recursively: %v", err)
	}
}

func TestFSCreateDirTargetIsFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "note.txt")
	writeTestFile(t, file)
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := postCreateDir(t, server.URL, server.Client(), map[string]any{"path": file})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "файл") {
		t.Fatalf("error = %q", message)
	}

	resp, body = postCreateDir(t, server.URL, server.Client(), map[string]any{
		"path": filepath.Join(file, "sub"),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("file component: status = %d, body = %v", resp.StatusCode, body)
	}
}

func TestFSCreateDirUnmountedRootIs500(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted")
	_, server, _ := newRootedServer(t, []string{missing}, missing)

	resp, body := postCreateDir(t, server.URL, server.Client(), map[string]any{
		"path": filepath.Join(missing, "new"),
	})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); message == "" {
		t.Fatalf("empty error: %v", body)
	}
}

func TestFSCreateDirRejectsBadJSON(t *testing.T) {
	root := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)

	cases := []map[string]any{
		{"path": 42},
		{"path": filepath.Join(root, "x"), "extra": true},
	}
	for _, payload := range cases {
		resp, body := postCreateDir(t, server.URL, server.Client(), payload)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%v: status = %d, body = %v", payload, resp.StatusCode, body)
		}
	}

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/fs/dirs", strings.NewReader(`{"path":`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed json: status = %d", resp.StatusCode)
	}
}

func TestFSCreateDirRequiresJSONContentType(t *testing.T) {
	root := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/fs/dirs",
		strings.NewReader(`{"path":"`+filepath.Join(root, "x")+`"}`))
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
