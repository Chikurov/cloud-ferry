package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"cloudferry/internal/jobs"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

// maxCloudSizePaths bounds one /api/cloud/sizes request so one UI action cannot
// fan out into an unbounded number of subtree walks.
const maxCloudSizePaths = 20

// cloudSizesParallelism limits concurrent operations/size calls. Every call
// walks a whole subtree in the provider, so a burst of twenty requests would be
// both slow and rude to the cloud.
const cloudSizesParallelism = 4

// cloudSizesTTL is how long one measured folder is served from memory. Sizing a
// folder means walking its whole tree, so sizes change slowly and the cache is
// deliberately longer than the listing cache.
const cloudSizesTTL = 10 * time.Minute

// maxCloudSizesCached bounds the in-memory size cache.
const maxCloudSizesCached = 512

// cloudSizesTimeout is the budget of one whole batch. Paths that are still
// running when it expires are reported as entries with an error, not as a
// failed request. It is a variable so tests do not have to wait for it.
var cloudSizesTimeout = 25 * time.Second

// Messages a single failed path can carry. The text is user-facing and never
// contains rclone internals.
const (
	cloudSizeNotFoundMessage = "Папка не найдена"
	cloudSizeTimeoutMessage  = "Не успели посчитать"
	cloudSizeFailedMessage   = "Не удалось посчитать"
)

// errCloudSizesTimeout means the caller's request context ended before the
// batch could answer: the whole request is reported as a timeout.
var errCloudSizesTimeout = errors.New("cloud sizes: request timeout")

// errCloudSizesUnavailable means rclone itself is not reachable. Sizing every
// path would fail, so the request is answered as an outage instead of a list of
// identical per-path errors.
var errCloudSizesUnavailable = errors.New("cloud sizes: engine unavailable")

// cloudSizesRequest is the body of POST /api/cloud/sizes.
type cloudSizesRequest struct {
	AccountID string   `json:"accountId"`
	Paths     []string `json:"paths"`
}

// cloudSize is one successful measurement: the file count and total bytes
// rclone reports for a folder and its subtree.
type cloudSize struct {
	Files int64
	Bytes int64
}

// cloudSizeEntry is one item of /api/cloud/sizes. Bytes and files are pointers
// so a failed path does not look like an empty folder; an empty folder still
// reports real zeros.
type cloudSizeEntry struct {
	Path  string `json:"path"`
	Bytes *int64 `json:"bytes,omitempty"`
	Files *int64 `json:"files,omitempty"`
	Error string `json:"error,omitempty"`
}

type cloudSizesResponse struct {
	Entries []cloudSizeEntry `json:"entries"`
}

// cloudSizesKey is the cache key: a folder of one account. Paths are stored
// normalised, so two spellings of the same folder share one entry.
type cloudSizesKey struct {
	accountID string
	path      string
}

type cachedCloudSize struct {
	at   time.Time
	size cloudSize
}

// cloudSizesCache keeps recent folder sizes so the folder dialog can show them
// without sizing the same tree on every click.
type cloudSizesCache struct {
	mu    sync.Mutex
	now   func() time.Time
	ttl   time.Duration
	items map[cloudSizesKey]cachedCloudSize
}

func newCloudSizesCache(now func() time.Time) *cloudSizesCache {
	return &cloudSizesCache{now: now, ttl: cloudSizesTTL, items: map[cloudSizesKey]cachedCloudSize{}}
}

func (c *cloudSizesCache) get(accountID, cloudPath string) (cloudSize, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[cloudSizesKey{accountID: accountID, path: cloudPath}]
	if !ok || c.now().Sub(item.at) > c.ttl {
		return cloudSize{}, false
	}
	return item.size, true
}

func (c *cloudSizesCache) put(accountID, cloudPath string, size cloudSize) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cloudSizesKey{accountID: accountID, path: cloudPath}
	now := c.now()
	for staleKey, item := range c.items {
		if now.Sub(item.at) > c.ttl {
			delete(c.items, staleKey)
		}
	}
	// Refreshing an existing entry must not evict a different folder.
	if _, exists := c.items[key]; !exists && len(c.items) >= maxCloudSizesCached {
		var oldestKey cloudSizesKey
		var oldest time.Time
		first := true
		for candidate, item := range c.items {
			if first || item.at.Before(oldest) {
				oldestKey, oldest, first = candidate, item.at, false
			}
		}
		delete(c.items, oldestKey)
	}
	c.items[key] = cachedCloudSize{at: now, size: size}
}

// handleCloudSizes measures a handful of cloud folders in one request. A folder
// that cannot be measured is reported inside its entry; only a broken request,
// an unknown account or an unreachable engine fails the whole answer.
func (s *Server) handleCloudSizes(w http.ResponseWriter, r *http.Request) {
	var req cloudSizesRequest
	if err := decodeJSON(r, &req); err != nil {
		s.badRequest(w, err)
		return
	}
	account, ok := s.cfg.Store.Account(strings.TrimSpace(req.AccountID))
	if !ok {
		writeError(w, http.StatusNotFound, "Подключение не найдено. Возможно, оно уже отключено.")
		return
	}
	paths, err := normalizeSizePaths(req.Paths)
	if err != nil {
		writeFieldError(w, http.StatusBadRequest, "paths", err.Error())
		return
	}

	entries, err := s.cloudSizes(r.Context(), account, paths)
	switch {
	case errors.Is(err, errCloudSizesTimeout):
		writeError(w, http.StatusGatewayTimeout, "Не успели посчитать. Попробуйте ещё раз.")
		return
	case errors.Is(err, errCloudSizesUnavailable):
		s.fail(w, http.StatusBadGateway, "Облако сейчас не отвечает. Попробуйте ещё раз через минуту.", err)
		return
	case err != nil:
		s.fail(w, http.StatusInternalServerError, "Не удалось посчитать объём. Попробуйте ещё раз.", err)
		return
	}
	writeJSON(w, http.StatusOK, cloudSizesResponse{Entries: entries})
}

// normalizeSizePaths validates every requested folder the same way a download
// source is validated and keeps the caller's order. The answer has exactly one
// entry per valid path, in the same order.
func normalizeSizePaths(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("Укажите хотя бы одну папку в облаке.")
	}
	if len(raw) > maxCloudSizePaths {
		return nil, fmt.Errorf("Можно посчитать не больше %d папок за один раз.", maxCloudSizePaths)
	}
	cleaned := make([]string, 0, len(raw))
	for _, item := range raw {
		value, err := normalizeSourcePath(item)
		if err != nil {
			return nil, err
		}
		cleaned = append(cleaned, value)
	}
	return cleaned, nil
}

// cloudSizes answers entries in the order the caller asked for them. Cached
// folders are served immediately; at most cloudSizesParallelism rclone calls
// run at once under one batch deadline.
func (s *Server) cloudSizes(ctx context.Context, account store.Account, paths []string) ([]cloudSizeEntry, error) {
	entries := make([]cloudSizeEntry, len(paths))
	pending := make([]int, 0, len(paths))
	for index, cloudPath := range paths {
		if size, ok := s.sizes.get(account.ID, cloudPath); ok {
			entries[index] = newCloudSizeEntry(cloudPath, size)
			continue
		}
		pending = append(pending, index)
	}
	if len(pending) == 0 {
		return entries, nil
	}

	batchCtx, cancel := context.WithTimeout(ctx, cloudSizesTimeout)
	defer cancel()

	// The channel is buffered for every pending path so a worker that finishes
	// after the caller gave up can still return and exit.
	results := make(chan cloudSizeResult, len(pending))
	semaphore := make(chan struct{}, cloudSizesParallelism)
	for _, index := range pending {
		index, cloudPath := index, paths[index]
		go func() {
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			size, err := s.cloudSizeOne(batchCtx, account, cloudPath)
			results <- cloudSizeResult{index: index, size: size, err: err}
		}()
	}

	done := make([]bool, len(paths))
	for finished := 0; finished < len(pending); {
		select {
		case result := <-results:
			finished++
			done[result.index] = true
			if result.err != nil {
				if isCloudSizesUnavailable(result.err) {
					// Keep the technical cause for the log, but answer with a
					// stable sentinel so the handler can classify it.
					return nil, fmt.Errorf("%w: %v", errCloudSizesUnavailable, result.err)
				}
				entries[result.index] = failedCloudSizeEntry(paths[result.index], result.err)
				continue
			}
			s.sizes.put(account.ID, paths[result.index], result.size)
			entries[result.index] = newCloudSizeEntry(paths[result.index], result.size)
		case <-batchCtx.Done():
			// Do not wait for the rest: the request budget is over. Workers
			// notice the cancelled context and exit on their own.
			finished = len(pending)
		}
	}

	for _, index := range pending {
		if !done[index] {
			entries[index] = cloudSizeEntry{Path: paths[index], Error: cloudSizeTimeoutMessage}
		}
	}
	// The batch deadline is ours, the request context is the caller's. Only the
	// latter means the whole answer cannot be delivered.
	if ctx.Err() != nil {
		return nil, errCloudSizesTimeout
	}
	return entries, nil
}

// cloudSizeOne asks rclone for the count and bytes of one folder subtree.
func (s *Server) cloudSizeOne(ctx context.Context, account store.Account, cloudPath string) (cloudSize, error) {
	var out struct {
		Count int64 `json:"count"`
		Bytes int64 `json:"bytes"`
	}
	req := map[string]any{"fs": account.Remote + ":" + cloudPath}
	if err := s.cfg.Rclone.Call(ctx, "operations/size", req, &out); err != nil {
		return cloudSize{}, err
	}
	return cloudSize{Files: out.Count, Bytes: out.Bytes}, nil
}

type cloudSizeResult struct {
	index int
	size  cloudSize
	err   error
}

// isCloudSizesUnavailable tells "rclone is not running" from a provider error
// about one folder. Context deadlines are timeouts, not outages.
func isCloudSizesUnavailable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	return rc.IsUnavailable(err)
}

// newCloudSizeEntry reports a measured folder. Separate values are taken so
// every entry owns its pointers.
func newCloudSizeEntry(cloudPath string, size cloudSize) cloudSizeEntry {
	bytes, files := size.Bytes, size.Files
	return cloudSizeEntry{Path: cloudPath, Bytes: &bytes, Files: &files}
}

// failedCloudSizeEntry names the reason a single folder could not be measured
// without exposing rclone internals.
func failedCloudSizeEntry(cloudPath string, err error) cloudSizeEntry {
	entry := cloudSizeEntry{Path: cloudPath, Error: cloudSizeFailedMessage}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		entry.Error = cloudSizeTimeoutMessage
	case jobs.IsMissingSource(err):
		entry.Error = cloudSizeNotFoundMessage
	}
	return entry
}
