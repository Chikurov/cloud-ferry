package httpapi

import (
	"errors"
	"net/http"

	"cloudferry/internal/fsbrowse"
)

// existingRequest asks which of the selected cloud folders already exist in
// the destination. Unlike a job request it never creates anything.
type existingRequest struct {
	AccountID   string   `json:"accountId"`
	DestPath    string   `json:"destPath"`
	SourcePaths []string `json:"sourcePaths"`
}

// existingItem is one selected cloud folder and its state in the destination.
// Entries is the number of top-level items of an existing folder, so zero is a
// folder that exists but is empty.
type existingItem struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Entries int    `json:"entries"`
}

type existingResponse struct {
	Dest  string         `json:"dest"`
	Items []existingItem `json:"items"`
}

// handleExistingFolders answers which selected cloud folders are already in the
// local destination. It only inspects the top level of each folder: no
// recursion, no sizes and no writes.
func (s *Server) handleExistingFolders(w http.ResponseWriter, r *http.Request) {
	var req existingRequest
	if err := decodeJSON(r, &req); err != nil {
		s.badRequest(w, err)
		return
	}
	if _, ok := s.cfg.Store.Account(req.AccountID); !ok {
		writeError(w, http.StatusNotFound, "Подключение не найдено. Возможно, оно уже отключено.")
		return
	}
	ordered, paths, err := normalizeExistingPaths(req.SourcePaths)
	if err != nil {
		s.sourceError(w, err)
		return
	}
	dest, err := s.cleanDestPath(req.DestPath)
	if err != nil {
		status := destErrorStatus(err)
		if status == http.StatusInternalServerError {
			// destPathError hides the cause from the user message; log it for
			// diagnostics instead.
			s.fail(w, status, "Не удалось проверить папку на сервере. Попробуйте ещё раз.", errors.Unwrap(err))
			return
		}
		writeFieldError(w, status, "destPath", err.Error())
		return
	}

	items := existingItems(ordered, paths)
	for i := range items {
		state, err := fsbrowse.ProbeDir(s.cfg.DestRoots, dest, items[i].Path)
		if err != nil {
			s.existingProbeError(w, err)
			return
		}
		items[i].Exists = state.Exists
		items[i].Entries = state.Entries
	}
	writeJSON(w, http.StatusOK, existingResponse{Dest: dest, Items: items})
}

// destErrorStatus maps a destination validation failure to the status this
// endpoint promises: an outside root or a symlink is forbidden, a malformed
// path is a bad request and an unexpected filesystem failure is ours.
func destErrorStatus(err error) int {
	switch {
	case errors.Is(err, fsbrowse.ErrOutsideRoots), errors.Is(err, fsbrowse.ErrSymlink):
		return http.StatusForbidden
	case errors.Is(err, fsbrowse.ErrInvalidPath), errors.Is(err, fsbrowse.ErrNotDir):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// normalizeExistingPaths validates the selection in request order, then hands
// it to the same normaliser jobs use: duplicates collapse, a selected ancestor
// covers its descendants and the limit applies to the surviving folders.
func normalizeExistingPaths(raw []string) (ordered, normalized []string, err error) {
	ordered = make([]string, 0, len(raw))
	for _, item := range raw {
		value, err := normalizeSourcePath(item)
		if err != nil {
			return nil, nil, &sourceFieldError{field: "sourcePaths", message: err.Error()}
		}
		ordered = append(ordered, value)
	}
	normalized, err = normalizeSourcePaths(ordered)
	if err != nil {
		return nil, nil, err
	}
	return ordered, normalized, nil
}

// existingItems keeps the order the client sent while dropping duplicates and
// descendants of a selected ancestor, so the answer matches what a job would
// actually transfer.
func existingItems(ordered, normalized []string) []existingItem {
	kept := make(map[string]struct{}, len(normalized))
	for _, value := range normalized {
		kept[value] = struct{}{}
	}
	seen := make(map[string]struct{}, len(ordered))
	items := make([]existingItem, 0, len(normalized))
	for _, value := range ordered {
		if _, dup := seen[value]; dup {
			continue
		}
		seen[value] = struct{}{}
		if _, ok := kept[value]; !ok {
			continue
		}
		items = append(items, existingItem{Path: value})
	}
	return items
}

func (s *Server) existingProbeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fsbrowse.ErrInvalidPath):
		writeFieldError(w, http.StatusBadRequest, "sourcePaths", "Проверьте путь в облаке.")
	case errors.Is(err, fsbrowse.ErrOutsideRoots), errors.Is(err, fsbrowse.ErrSymlink):
		writeError(w, http.StatusForbidden, s.destError(err).Error())
	default:
		s.fail(w, http.StatusInternalServerError, "Не удалось проверить папки на сервере. Попробуйте ещё раз.", err)
	}
}
