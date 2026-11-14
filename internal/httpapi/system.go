package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"cloudferry/internal/disk"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
)

// cloudInfoTTL is how long the cloud usage snapshot is considered fresh.
const cloudInfoTTL = 60 * time.Second

// Cloud statuses reported in /api/system.
const (
	cloudStatusOK          = "ok"
	cloudStatusNeedsAuth   = "needs-auth"
	cloudStatusUnavailable = "unavailable"
	cloudStatusEmpty       = "empty"
)

// cloudInfo is the additive cloud object of /api/system. Numeric fields are
// pointers so unavailable data is null, not a deceptive zero.
type cloudInfo struct {
	AccountID string     `json:"accountId,omitempty"`
	Name      string     `json:"name,omitempty"`
	Provider  string     `json:"provider,omitempty"`
	Status    string     `json:"status"`
	Total     *int64     `json:"total"`
	Used      *int64     `json:"used"`
	Free      *int64     `json:"free"`
	Available bool       `json:"available"`
	Stale     bool       `json:"stale"`
	UpdatedAt *time.Time `json:"updatedAt"`
}

// cloudInformer serves the last known cloud usage and refreshes it in the
// background, so /api/system never waits for the cloud provider.
type cloudInformer struct {
	store *store.Store
	rc    Engine
	now   func() time.Time
	ttl   time.Duration

	mu         sync.Mutex
	info       cloudInfo
	have       bool
	at         time.Time
	refreshing bool
	inflight   sync.WaitGroup
}

func newCloudInformer(st *store.Store, engine Engine, now func() time.Time) *cloudInformer {
	return &cloudInformer{store: st, rc: engine, now: now, ttl: cloudInfoTTL}
}

// snapshot returns the cached state immediately. A missing or outdated entry
// keeps the last known status and is marked stale.
func (c *cloudInformer) snapshot() cloudInfo {
	accounts := c.store.Accounts()
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(accounts) == 0 {
		return cloudInfo{Status: cloudStatusEmpty, Stale: true}
	}
	account := accounts[0]
	if !c.have || c.info.AccountID != account.ID {
		return cloudInfo{
			AccountID: account.ID,
			Name:      account.Name,
			Provider:  account.Provider,
			Status:    cloudStatusUnavailable,
			Stale:     true,
		}
	}
	info := c.info
	info.Stale = c.now().Sub(c.at) > c.ttl
	return info
}

// ensureRefresh starts one background fetch when the snapshot is stale. It
// never touches the network itself.
func (c *cloudInformer) ensureRefresh() {
	accounts := c.store.Accounts()
	if len(accounts) == 0 {
		return
	}
	account := accounts[0]

	c.mu.Lock()
	fresh := c.have && c.info.AccountID == account.ID && c.now().Sub(c.at) <= c.ttl
	if fresh || c.refreshing {
		c.mu.Unlock()
		return
	}
	c.refreshing = true
	c.inflight.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.inflight.Done()
		defer func() {
			c.mu.Lock()
			c.refreshing = false
			c.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c.refresh(ctx)
	}()
}

// refresh updates the snapshot once. Tests call it directly to keep the clock
// deterministic.
func (c *cloudInformer) refresh(ctx context.Context) {
	accounts := c.store.Accounts()
	now := c.now()
	info := cloudInfo{Status: cloudStatusEmpty}
	if len(accounts) == 0 {
		c.mu.Lock()
		c.info, c.have, c.at = info, false, now
		c.mu.Unlock()
		return
	}

	account := accounts[0]
	info = cloudInfo{AccountID: account.ID, Name: account.Name, Provider: account.Provider}
	var about struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
		Free  int64 `json:"free"`
	}
	err := c.rc.Call(ctx, "operations/about", map[string]any{"fs": account.Remote + ":"}, &about)
	switch {
	case err != nil && needsAuth(err):
		info.Status = cloudStatusNeedsAuth
	case err != nil:
		info.Status = cloudStatusUnavailable
	case about.Total == 0 && about.Used == 0 && about.Free == 0:
		info.Status = cloudStatusEmpty
	default:
		total, used, free := about.Total, about.Used, about.Free
		updated := now
		info.Status = cloudStatusOK
		info.Total, info.Used, info.Free = &total, &used, &free
		info.Available = true
		info.UpdatedAt = &updated
	}

	c.mu.Lock()
	c.info, c.have, c.at = info, true, now
	c.mu.Unlock()
}

// needsAuth reports whether a cloud error means the credentials are gone.
// Quota and network errors are not auth failures: the UI must not send the user
// through the login flow for them.
func needsAuth(err error) bool {
	var apiErr *rc.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusGone {
		return true
	}
	lower := strings.ToLower(apiErr.Body)
	for _, marker := range []string{"401", "410", "unauthorized"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// handleSystem reports where files land, how much room is left and what the
// cloud says about itself. The cloud part comes from the cache.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	type root struct {
		Path  string `json:"path"`
		Free  uint64 `json:"free"`
		Total uint64 `json:"total"`
	}
	roots := make([]root, 0, len(s.cfg.DestRoots))
	for _, destPath := range s.cfg.DestRoots {
		free, total, err := disk.Usage(destPath)
		if err != nil {
			continue
		}
		roots = append(roots, root{Path: destPath, Free: free, Total: total})
	}

	cloud := s.cloud.snapshot()
	s.cloud.ensureRefresh()

	writeJSON(w, http.StatusOK, map[string]any{
		"downloads": s.cfg.Downloads,
		"roots":     roots,
		"cloud":     cloud,
		"version":   s.cfg.Version,
	})
}
