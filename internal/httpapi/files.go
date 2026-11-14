package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cloudferry/internal/jobs"
	"cloudferry/internal/store"
)

// cloudFilesTTL is how long one cloud folder file listing is served from
// memory. The list changes rarely and the user clicks through folders quickly,
// so a short cache removes the repeated provider round trips.
const cloudFilesTTL = 2 * time.Minute

// maxCloudFilesCached bounds the in-memory file listing cache.
const maxCloudFilesCached = 256

// Default and maximum page size of /api/accounts/{id}/files.
const (
	defaultCloudFilesLimit = 50
	maxCloudFilesLimit     = 200
)

// cloudFilesTimeout is the budget of one listing request. It is a variable so
// tests do not have to wait for the real twenty seconds.
var cloudFilesTimeout = 20 * time.Second

// cloudFile is one file inside a cloud folder. Modified is RFC3339 (UTC,
// seconds precision) and omitted when rclone did not report a time.
type cloudFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified,omitempty"`
}

// cloudFilesResponse is the answer of /api/accounts/{id}/files. Only files are
// listed: the folders are what /api/accounts/{id}/folders is for. Count is the
// number of files in this answer, so it is never larger than the limit.
type cloudFilesResponse struct {
	Path      string      `json:"path"`
	Files     []cloudFile `json:"files"`
	Truncated bool        `json:"truncated"`
	Count     int         `json:"count"`
}

// cloudFilesKey is the cache key: one folder of one account at one page size.
// Paths are stored normalised, so two spellings of the same folder share an
// entry. The limit is part of the key because the answer is already truncated.
type cloudFilesKey struct {
	accountID string
	path      string
	limit     int
}

type cachedCloudFiles struct {
	at     time.Time
	answer cloudFilesResponse
}

// cloudFilesCache keeps recent file listings so the picker does not hit the
// provider on every click. Only successful answers are stored.
type cloudFilesCache struct {
	mu    sync.Mutex
	now   func() time.Time
	ttl   time.Duration
	items map[cloudFilesKey]cachedCloudFiles
}

func newCloudFilesCache(now func() time.Time) *cloudFilesCache {
	return &cloudFilesCache{now: now, ttl: cloudFilesTTL, items: map[cloudFilesKey]cachedCloudFiles{}}
}

// get returns a cached answer. The slice inside is shared read-only: callers
// must not mutate it.
func (c *cloudFilesCache) get(accountID, cloudPath string, limit int) (cloudFilesResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[cloudFilesKey{accountID: accountID, path: cloudPath, limit: limit}]
	if !ok || c.now().Sub(item.at) > c.ttl {
		return cloudFilesResponse{}, false
	}
	return item.answer, true
}

func (c *cloudFilesCache) put(accountID, cloudPath string, limit int, answer cloudFilesResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cloudFilesKey{accountID: accountID, path: cloudPath, limit: limit}
	now := c.now()
	for staleKey, item := range c.items {
		if now.Sub(item.at) > c.ttl {
			delete(c.items, staleKey)
		}
	}
	// Refreshing an existing entry must not evict a different folder.
	if _, exists := c.items[key]; !exists && len(c.items) >= maxCloudFilesCached {
		var oldestKey cloudFilesKey
		var oldest time.Time
		first := true
		for candidate, item := range c.items {
			if first || item.at.Before(oldest) {
				oldestKey, oldest, first = candidate, item.at, false
			}
		}
		delete(c.items, oldestKey)
	}
	c.items[key] = cachedCloudFiles{at: now, answer: answer}
}

// handleCloudFiles lists the files of one cloud folder so the user can see what
// a download will contain. It is read-only: the only rclone call is
// operations/list.
func (s *Server) handleCloudFiles(w http.ResponseWriter, r *http.Request) {
	account, ok := s.cfg.Store.Account(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Подключение не найдено. Возможно, оно уже отключено.")
		return
	}
	cloudPath, err := cleanCloudFilesPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := parseCloudFilesLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if answer, ok := s.files.get(account.ID, cloudPath, limit); ok {
		writeJSON(w, http.StatusOK, answer)
		return
	}

	answer, err := s.listCloudFiles(r.Context(), account, cloudPath, limit)
	switch {
	case err == nil:
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		writeError(w, http.StatusGatewayTimeout, "Облако не ответило вовремя. Попробуйте ещё раз.")
		return
	case jobs.IsMissingSource(err):
		writeError(w, http.StatusNotFound, "Папка не найдена в облаке. Возможно, её переименовали или удалили.")
		return
	default:
		s.fail(w, http.StatusBadGateway, "Не удалось прочитать файлы в облаке. Попробуйте ещё раз.", err)
		return
	}

	s.files.put(account.ID, cloudPath, limit, answer)
	writeJSON(w, http.StatusOK, answer)
}

// cleanCloudFilesPath validates a path relative to the cloud root exactly like
// a download source. An empty path is the root of the remote.
func cleanCloudFilesPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	return normalizeSourcePath(raw)
}

// parseCloudFilesLimit reads the optional page size. A value above the maximum
// is clamped instead of rejected: the reader asked for "as much as possible".
func parseCloudFilesLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultCloudFilesLimit, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, errors.New("Укажите число файлов от 1 до 200.")
	}
	if value > maxCloudFilesLimit {
		value = maxCloudFilesLimit
	}
	return value, nil
}

// listCloudFiles asks rclone for the direct children of one folder and keeps
// only the files, sorted by name and cut to the limit.
func (s *Server) listCloudFiles(parent context.Context, account store.Account, cloudPath string, limit int) (cloudFilesResponse, error) {
	ctx, cancel := context.WithTimeout(parent, cloudFilesTimeout)
	defer cancel()

	var listing struct {
		List []struct {
			Name    string        `json:"Name"`
			Size    int64         `json:"Size"`
			IsDir   bool          `json:"IsDir"`
			ModTime rcloneModTime `json:"ModTime"`
		} `json:"list"`
	}
	req := map[string]any{
		"fs":     account.Remote + ":",
		"remote": cloudPath,
		"opt":    map[string]any{"showHash": false},
	}
	if err := s.cfg.Rclone.Call(ctx, "operations/list", req, &listing); err != nil {
		return cloudFilesResponse{}, err
	}

	files := make([]cloudFile, 0, len(listing.List))
	for _, item := range listing.List {
		if item.IsDir || item.Name == "" {
			continue
		}
		file := cloudFile{Name: item.Name, Size: item.Size}
		if modified := formatRcloneModTime(item.ModTime); modified != "" {
			file.Modified = modified
		}
		files = append(files, file)
	}
	// Same order as the folder picker: case-insensitive name, stable tie-break.
	sort.Slice(files, func(i, j int) bool {
		left, right := strings.ToLower(files[i].Name), strings.ToLower(files[j].Name)
		if left == right {
			return files[i].Name < files[j].Name
		}
		return left < right
	})

	truncated := false
	if len(files) > limit {
		files = files[:limit]
		truncated = true
	}
	return cloudFilesResponse{Path: cloudPath, Files: files, Truncated: truncated, Count: len(files)}, nil
}

// rcloneModTime decodes rclone's ModTime, which is an RFC3339 timestamp with
// nanoseconds or null. Any other JSON type is dropped instead of failing the
// whole listing.
type rcloneModTime string

func (m *rcloneModTime) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		*m = ""
		return nil
	}
	*m = rcloneModTime(value)
	return nil
}

// formatRcloneModTime normalises rclone's timestamp to RFC3339 in UTC. An empty
// or unparsable value is omitted rather than exposed: the user only needs a
// human date, and a broken time must not fail the listing.
func formatRcloneModTime(value rcloneModTime) string {
	raw := strings.TrimSpace(string(value))
	if raw == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return ""
	}
	return parsed.UTC().Format(time.RFC3339)
}
