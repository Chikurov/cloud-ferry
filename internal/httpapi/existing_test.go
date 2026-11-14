package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func postExisting(t *testing.T, server *httptest.Server, body any) (*http.Response, map[string]any) {
	t.Helper()
	return doJSON(t, server.Client(), http.MethodPost, server.URL+"/api/jobs/existing", body)
}

func existingItemsFrom(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items = %#v", body["items"])
	}
	items := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		item, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("item = %#v", value)
		}
		items = append(items, item)
	}
	return items
}

func TestExistingFolders(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "downloads", "yandex")
	mkdirAll(t, filepath.Join(dest, "Фото", "2024"))
	writeTestFile(t, filepath.Join(dest, "Фото", "2024", "inner.jpg"))
	writeTestFile(t, filepath.Join(dest, "Фото", "a.jpg"))
	writeTestFile(t, filepath.Join(dest, "Фото", "b.jpg"))
	mkdirAll(t, filepath.Join(dest, "Пусто"))
	mkdirAll(t, filepath.Join(dest, "Документы", "2024"))
	mkdirAll(t, filepath.Join(dest, "Документы", "архив"))
	writeTestFile(t, filepath.Join(dest, "Документы", "2024", "отчёт.pdf"))

	_, server, engine := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	resp, body := postExisting(t, server, map[string]any{
		"accountId":   accountID,
		"destPath":    dest,
		"sourcePaths": []string{"Фото", "Пусто", "Документы/2024", "Пропавшая"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["dest"] != dest {
		t.Fatalf("dest = %v, want %q", body["dest"], dest)
	}

	items := existingItemsFrom(t, body)
	want := []struct {
		path    string
		exists  bool
		entries int
	}{
		{"Фото", true, 3},
		{"Пусто", true, 0},
		{"Документы/2024", true, 1},
		{"Пропавшая", false, 0},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %v, want %d", items, len(want))
	}
	for i, expected := range want {
		if items[i]["path"] != expected.path ||
			items[i]["exists"] != expected.exists ||
			items[i]["entries"] != float64(expected.entries) {
			t.Fatalf("item %d = %v, want %+v", i, items[i], expected)
		}
	}

	if _, err := os.Stat(filepath.Join(dest, "Пропавшая")); !os.IsNotExist(err) {
		t.Fatalf("missing folder was created: %v", err)
	}
	if engine.count("operations/list") != 0 || engine.count("sync/copy") != 0 {
		t.Fatalf("existing check must not call rclone: %d list, %d copy",
			engine.count("operations/list"), engine.count("sync/copy"))
	}
}

func TestExistingFoldersDoesNotCreateDestination(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "new", "dest")
	_, server, _ := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	resp, body := postExisting(t, server, map[string]any{
		"accountId":   accountID,
		"destPath":    dest,
		"sourcePaths": []string{"Фото", "Документы/2024"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["dest"] != dest {
		t.Fatalf("dest = %v, want %q", body["dest"], dest)
	}
	items := existingItemsFrom(t, body)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	for _, item := range items {
		if item["exists"] != false || item["entries"] != float64(0) {
			t.Fatalf("item = %v, want not existing", item)
		}
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("destination was created: %v", err)
	}
}

func TestExistingFoldersNormalisesSelection(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	mkdirAll(t, filepath.Join(dest, "Фото"))
	mkdirAll(t, filepath.Join(dest, "B", "sub"))
	_, server, _ := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	resp, body := postExisting(t, server, map[string]any{
		"accountId": accountID,
		"destPath":  dest + "/",
		"sourcePaths": []string{
			"/Фото/", " Фото ", "Фото/2024", "B//sub", "B/sub/",
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if body["dest"] != dest {
		t.Fatalf("dest = %v, want %q", body["dest"], dest)
	}
	items := existingItemsFrom(t, body)
	if len(items) != 2 {
		t.Fatalf("items = %v, want two normalised folders", items)
	}
	if items[0]["path"] != "Фото" || items[1]["path"] != "B/sub" {
		t.Fatalf("items = %v, want request order [Фото B/sub]", items)
	}
	for _, item := range items {
		if item["exists"] != true {
			t.Fatalf("item = %v, want existing", item)
		}
	}
}

func TestExistingFoldersEmptySelection(t *testing.T) {
	root := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	for _, payload := range []map[string]any{
		{"accountId": accountID, "destPath": root, "sourcePaths": []string{}},
		{"accountId": accountID, "destPath": root},
	} {
		resp, body := postExisting(t, server, payload)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%v: status = %d, body = %v", payload, resp.StatusCode, body)
		}
		if body["dest"] != root {
			t.Fatalf("%v: dest = %v, want %q", payload, body["dest"], root)
		}
		if items := existingItemsFrom(t, body); len(items) != 0 {
			t.Fatalf("%v: items = %v, want empty", payload, items)
		}
	}
}

func TestExistingFoldersLimit(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	mkdirAll(t, dest)
	_, server, _ := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	paths := make([]string, 0, 21)
	for i := 0; i < 21; i++ {
		paths = append(paths, fmt.Sprintf("папка-%02d", i))
	}

	resp, body := postExisting(t, server, map[string]any{
		"accountId":   accountID,
		"destPath":    dest,
		"sourcePaths": paths[:20],
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("20 folders: status = %d, body = %v", resp.StatusCode, body)
	}
	if items := existingItemsFrom(t, body); len(items) != 20 {
		t.Fatalf("20 folders: items = %d", len(items))
	}

	resp, body = postExisting(t, server, map[string]any{
		"accountId":   accountID,
		"destPath":    dest,
		"sourcePaths": paths,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("21 folders: status = %d, body = %v", resp.StatusCode, body)
	}
	if body["field"] != "sourcePaths" {
		t.Fatalf("field = %v", body["field"])
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "не больше 20 папок") {
		t.Fatalf("error = %q", message)
	}
}

func TestExistingFoldersDestinationErrors(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "note.txt")
	writeTestFile(t, file)
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	symlink(t, outside, link)
	_, server, _ := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	cases := []struct {
		name     string
		dest     string
		status   int
		contains string
	}{
		{"relative", "downloads/yandex", http.StatusBadRequest, "Укажите полный путь"},
		{"file", file, http.StatusBadRequest, "файл"},
		{"outside roots", filepath.Join(outside, "dest"), http.StatusForbidden, "сохранять нельзя"},
		{"symlink", link, http.StatusForbidden, "ведёт за пределы"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := postExisting(t, server, map[string]any{
				"accountId":   accountID,
				"destPath":    tc.dest,
				"sourcePaths": []string{"Фото"},
			})
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d, body = %v", resp.StatusCode, tc.status, body)
			}
			if message, _ := body["error"].(string); !strings.Contains(message, tc.contains) {
				t.Fatalf("error = %q, want %q", message, tc.contains)
			}
			if body["field"] != "destPath" {
				t.Fatalf("field = %v", body["field"])
			}
		})
	}
}

func TestExistingFoldersSymlinkSourceIsNotExisting(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	target := filepath.Join(dest, "Настоящая")
	mkdirAll(t, target)
	writeTestFile(t, filepath.Join(target, "a.txt"))
	symlink(t, target, filepath.Join(dest, "Ссылка"))
	_, server, _ := newRootedServer(t, []string{root}, root)
	accountID := createTestAccount(t, server)

	resp, body := postExisting(t, server, map[string]any{
		"accountId":   accountID,
		"destPath":    dest,
		"sourcePaths": []string{"Ссылка", "Настоящая"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	items := existingItemsFrom(t, body)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	if items[0]["path"] != "Ссылка" || items[0]["exists"] != false || items[0]["entries"] != float64(0) {
		t.Fatalf("symlink item = %v, want not existing", items[0])
	}
	if items[1]["path"] != "Настоящая" || items[1]["exists"] != true || items[1]["entries"] != float64(1) {
		t.Fatalf("real item = %v, want existing with one entry", items[1])
	}
}

func TestExistingFoldersUnknownAccount(t *testing.T) {
	root := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)

	resp, body := postExisting(t, server, map[string]any{
		"accountId":   "missing",
		"destPath":    root,
		"sourcePaths": []string{"Фото"},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, body = %v", resp.StatusCode, body)
	}
	if message, _ := body["error"].(string); !strings.Contains(message, "Подключение не найдено") {
		t.Fatalf("error = %q", message)
	}
}

func TestExistingFoldersStrictJSON(t *testing.T) {
	root := t.TempDir()
	_, server, _ := newRootedServer(t, []string{root}, root)

	payloads := []map[string]any{
		{"destPath": root, "sourcePaths": []string{}, "extra": true},
		{"destPath": root, "sourcePaths": "Фото"},
		{"destPath": root, "sourcePath": "Фото"},
	}
	for _, payload := range payloads {
		resp, body := postExisting(t, server, payload)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%v: status = %d, body = %v", payload, resp.StatusCode, body)
		}
		if message, _ := body["error"].(string); message == "" {
			t.Fatalf("%v: empty error", payload)
		}
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/jobs/existing",
		strings.NewReader(`{"sourcePaths":`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
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
