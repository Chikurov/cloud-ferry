package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"cloudferry/internal/fsbrowse"
	"cloudferry/internal/store"
)

// cloudFoldersTTL is how long one cloud folder listing is served from memory.
const cloudFoldersTTL = 60 * time.Second

// maxCloudFoldersCached bounds the in-memory listing cache.
const maxCloudFoldersCached = 256

// dirEntry is one folder in a browse response.
type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// fsDirsResponse is the shared answer of /api/fs/dirs and /api/fs/folders.
// Dirs, root and parent are the agreed contract; entries and roots keep the
// already shipped UI working without changing app.js.
type fsDirsResponse struct {
	Root      string     `json:"root"`
	Path      string     `json:"path"`
	Parent    *string    `json:"parent"`
	Dirs      []dirEntry `json:"dirs"`
	Entries   []dirEntry `json:"entries"`
	Roots     []string   `json:"roots"`
	Free      uint64     `json:"free"`
	Total     uint64     `json:"total"`
	Truncated bool       `json:"truncated"`
	Writable  bool       `json:"writable"`
}

func (s *Server) handleFSDirs(w http.ResponseWriter, r *http.Request) {
	requested := strings.TrimSpace(r.URL.Query().Get("path"))
	if requested == "" {
		requested = s.defaultDestRoot()
	}
	listing, err := fsbrowse.Browse(s.cfg.DestRoots, requested)
	if err != nil {
		s.fsBrowseError(w, err)
		return
	}

	dirs := make([]dirEntry, 0, len(listing.Dirs))
	for _, entry := range listing.Dirs {
		dirs = append(dirs, dirEntry{Name: entry.Name, Path: entry.Path})
	}
	roots := []string{listing.Root}
	for _, root := range s.cfg.DestRoots {
		cleaned := path.Clean(root)
		if cleaned != listing.Root {
			roots = append(roots, cleaned)
		}
	}
	writeJSON(w, http.StatusOK, fsDirsResponse{
		Root:      listing.Root,
		Path:      listing.Path,
		Parent:    listing.Parent,
		Dirs:      dirs,
		Entries:   dirs,
		Roots:     roots,
		Free:      listing.Free,
		Total:     listing.Total,
		Truncated: listing.Truncated,
		Writable:  true,
	})
}

// defaultDestRoot is the folder the picker opens when no path is given.
func (s *Server) defaultDestRoot() string {
	downloads := path.Clean(s.cfg.Downloads)
	for _, root := range s.cfg.DestRoots {
		if path.Clean(root) == downloads {
			return downloads
		}
	}
	return path.Clean(s.cfg.DestRoots[0])
}

type createDirRequest struct {
	Path string `json:"path"`
}

// handleFSCreateDir creates one folder on the server inside the allowed roots.
// The parent must already exist: this is a single mkdir, not a recursive one.
func (s *Server) handleFSCreateDir(w http.ResponseWriter, r *http.Request) {
	var req createDirRequest
	if err := decodeJSON(r, &req); err != nil {
		s.badRequest(w, err)
		return
	}
	created, err := fsbrowse.Mkdir(s.cfg.DestRoots, req.Path)
	if err != nil {
		s.fsMkdirError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": created})
}

func (s *Server) fsMkdirError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fsbrowse.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, "Укажите полный путь на сервере, например /downloads.")
	case errors.Is(err, fsbrowse.ErrOutsideRoots):
		writeError(w, http.StatusForbidden,
			fmt.Sprintf("В эту папку создавать нельзя. Доступные папки: %s.", strings.Join(s.cfg.DestRoots, ", ")))
	case errors.Is(err, fsbrowse.ErrSymlink):
		writeError(w, http.StatusForbidden, "Эта папка ведёт за пределы доступной. Выберите другую.")
	case errors.Is(err, fsbrowse.ErrNotFound):
		writeError(w, http.StatusNotFound, "Папка не найдена. Выберите существующую папку и создайте новую внутри неё.")
	case errors.Is(err, fsbrowse.ErrNotDir):
		writeError(w, http.StatusBadRequest, "Это файл, а не папка.")
	case errors.Is(err, fsbrowse.ErrExists):
		writeError(w, http.StatusConflict, "Папка уже существует.")
	default:
		s.fail(w, http.StatusInternalServerError, "Не удалось создать папку на сервере. Проверьте, что диск подключён.", err)
	}
}

func (s *Server) fsBrowseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fsbrowse.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, "Укажите полный путь на сервере, например /downloads.")
	case errors.Is(err, fsbrowse.ErrOutsideRoots):
		writeError(w, http.StatusForbidden,
			fmt.Sprintf("В эту папку открывать нельзя. Доступные папки: %s.", strings.Join(s.cfg.DestRoots, ", ")))
	case errors.Is(err, fsbrowse.ErrSymlink):
		writeError(w, http.StatusForbidden, "Эта папка ведёт за пределы доступной. Выберите другую.")
	case errors.Is(err, fsbrowse.ErrNotFound):
		writeError(w, http.StatusNotFound, "Папка не найдена. Возможно, её переименовали или удалили.")
	case errors.Is(err, fsbrowse.ErrNotDir):
		writeError(w, http.StatusBadRequest, "Это файл, а не папка.")
	default:
		s.fail(w, http.StatusInternalServerError, "Папка на сервере недоступна. Проверьте, что диск подключён.", err)
	}
}

// cloudDirListing is the cached part of a cloud folder listing.
type cloudDirListing struct {
	Path      string
	Parent    *string
	Dirs      []dirEntry
	Truncated bool
}

type cachedCloudDirs struct {
	at      time.Time
	listing cloudDirListing
}

// cloudFoldersCache keeps recent cloud listings so the folder dialog does not
// hit the provider on every click.
type cloudFoldersCache struct {
	mu    sync.Mutex
	now   func() time.Time
	ttl   time.Duration
	items map[string]cachedCloudDirs
}

func newCloudFoldersCache(now func() time.Time) *cloudFoldersCache {
	return &cloudFoldersCache{now: now, ttl: cloudFoldersTTL, items: map[string]cachedCloudDirs{}}
}

func (c *cloudFoldersCache) get(accountID, cloudPath string) (cloudDirListing, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[accountID+"\x00"+cloudPath]
	if !ok || c.now().Sub(item.at) > c.ttl {
		return cloudDirListing{}, false
	}
	return item.listing, true
}

func (c *cloudFoldersCache) put(accountID, cloudPath string, listing cloudDirListing) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for key, item := range c.items {
		if now.Sub(item.at) > c.ttl {
			delete(c.items, key)
		}
	}
	if len(c.items) >= maxCloudFoldersCached {
		var oldestKey string
		var oldest time.Time
		for key, item := range c.items {
			if oldestKey == "" || item.at.Before(oldest) {
				oldestKey, oldest = key, item.at
			}
		}
		delete(c.items, oldestKey)
	}
	c.items[accountID+"\x00"+cloudPath] = cachedCloudDirs{at: now, listing: listing}
}

// cloudFoldersResponse is the answer of /api/accounts/{id}/folders. Entries is
// the alias used by app.js; dirs is the agreed field.
type cloudFoldersResponse struct {
	AccountID string     `json:"accountId"`
	Path      string     `json:"path"`
	Parent    *string    `json:"parent"`
	Dirs      []dirEntry `json:"dirs"`
	Entries   []dirEntry `json:"entries"`
	Truncated bool       `json:"truncated"`
}

func (s *Server) handleCloudFolders(w http.ResponseWriter, r *http.Request) {
	account, ok := s.cfg.Store.Account(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Подключение не найдено. Возможно, оно уже отключено.")
		return
	}
	src, err := cleanSourcePath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	listing, ok := s.folders.get(account.ID, src)
	if !ok {
		listing, err = s.listCloudDirs(r, account, src)
		if err != nil {
			s.fail(w, http.StatusBadGateway, "Не удалось прочитать папки в облаке. Попробуйте ещё раз.", err)
			return
		}
		s.folders.put(account.ID, src, listing)
	}

	writeJSON(w, http.StatusOK, cloudFoldersResponse{
		AccountID: account.ID,
		Path:      listing.Path,
		Parent:    listing.Parent,
		Dirs:      listing.Dirs,
		Entries:   listing.Dirs,
		Truncated: listing.Truncated,
	})
}

func (s *Server) listCloudDirs(r *http.Request, account store.Account, src string) (cloudDirListing, error) {
	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()

	var listing struct {
		List []struct {
			Name  string `json:"Name"`
			IsDir bool   `json:"IsDir"`
		} `json:"list"`
	}
	req := map[string]any{
		"fs":     account.Remote + ":",
		"remote": src,
		"opt":    map[string]any{"dirsOnly": true},
	}
	if err := s.cfg.Rclone.Call(ctx, "operations/list", req, &listing); err != nil {
		return cloudDirListing{}, err
	}

	dirs := make([]dirEntry, 0, len(listing.List))
	for _, item := range listing.List {
		if !item.IsDir || item.Name == "" {
			continue
		}
		dirs = append(dirs, dirEntry{Name: item.Name, Path: path.Join(src, item.Name)})
	}
	sort.Slice(dirs, func(i, j int) bool {
		left, right := strings.ToLower(dirs[i].Name), strings.ToLower(dirs[j].Name)
		if left == right {
			return dirs[i].Name < dirs[j].Name
		}
		return left < right
	})
	truncated := false
	if len(dirs) > fsbrowse.MaxDirs {
		dirs = dirs[:fsbrowse.MaxDirs]
		truncated = true
	}

	result := cloudDirListing{Path: src, Dirs: dirs, Truncated: truncated}
	if src != "" {
		parent := path.Dir(src)
		if parent == "." {
			parent = ""
		}
		result.Parent = &parent
	}
	return result, nil
}
