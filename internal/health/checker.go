// Package health periodically checks that connected clouds still accept the
// stored credentials, so the UI can warn before a transfer fails.
package health

import (
	"context"
	"log"
	"strings"
	"time"

	"cloudferry/internal/store"
)

// Caller is the rclone surface the checker needs.
type Caller interface {
	Call(ctx context.Context, method string, in, out any) error
}

// Checker re-validates every account on an interval.
type Checker struct {
	store *store.Store
	rc    Caller
	every time.Duration
}

// New builds a checker. A zero interval means 30 minutes.
func New(st *store.Store, rc Caller, every time.Duration) *Checker {
	if every <= 0 {
		every = 30 * time.Minute
	}
	return &Checker{store: st, rc: rc, every: every}
}

// Run checks once at start and then on every tick until ctx is done.
func (c *Checker) Run(ctx context.Context) {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			c.CheckAll(ctx)
			timer.Reset(c.every)
		}
	}
}

// CheckAll validates every account once.
func (c *Checker) CheckAll(ctx context.Context) {
	for _, account := range c.store.Accounts() {
		c.CheckOne(ctx, account)
	}
}

// CheckOne validates one account and records the result.
func (c *Checker) CheckOne(ctx context.Context, account store.Account) {
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var about struct {
		Total int64 `json:"total"`
	}
	err := c.rc.Call(callCtx, "operations/about", map[string]any{"fs": account.Remote + ":"}, &about)

	status := store.AccountStatusOK
	note := ""
	if err != nil {
		if !IsAuthError(err.Error()) {
			return
		}
		status = store.AccountStatusAuth
		note = "Нужно подключить облако заново"
	}
	if updateErr := c.store.UpdateAccount(account.ID, func(a *store.Account) bool {
		a.Status = status
		a.StatusNote = note
		a.CheckedAt = time.Now().UTC()
		return true
	}); updateErr != nil {
		log.Printf("health: update %s: %v", account.ID, updateErr)
	}
}

// IsAuthError reports whether an rclone error means the credentials are gone.
func IsAuthError(message string) bool {
	lower := strings.ToLower(message)
	for _, marker := range []string{"401", "403", "410", "unauthorized", "forbidden", "gone"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
