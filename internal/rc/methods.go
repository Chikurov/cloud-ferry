package rc

import (
	"errors"
	"sort"
)

// ErrMethodNotAllowed is returned for rc methods outside the allowlist.
var ErrMethodNotAllowed = errors.New("rc method not allowed")

// allowedMethods is the complete surface this app may use. Everything else is
// rejected before the request reaches rclone: the rc API can run commands,
// dump credentials and read arbitrary files, so it must never be proxied
// wholesale.
//
// Deliberately absent: core/command, core/quit, config/dump, config/setpath,
// config/unlock, operations/getfile, operations/uploadfile, operations/purge,
// mount/*, serve/*, vfs/*, debug/*, backend/command.
var allowedMethods = map[string]struct{}{
	// jobs
	"sync/copy":  {},
	"sync/sync":  {},
	"job/status": {},
	"job/stop":   {},

	// progress
	"core/stats":   {},
	"core/version": {},

	// browsing and sizing
	"operations/list":  {},
	"operations/stat":  {},
	"operations/size":  {},
	"operations/about": {},

	// integrity
	"operations/check": {},

	// remotes
	"config/create": {},
	"config/delete": {},
	"config/get":    {},
}

// Allowed reports whether method may be called.
func Allowed(method string) bool {
	_, ok := allowedMethods[method]
	return ok
}

// AllowedMethods returns the allowlist sorted, for docs and tests.
func AllowedMethods() []string {
	out := make([]string, 0, len(allowedMethods))
	for m := range allowedMethods {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
