// Package httpapi is the browser-facing layer. It never exposes rclone's rc
// API: every call goes through typed handlers and the rc allowlist.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"cloudferry/internal/disk"
	"cloudferry/internal/fsbrowse"
	"cloudferry/internal/health"
	"cloudferry/internal/jobs"
	"cloudferry/internal/rc"
	"cloudferry/internal/store"
	"cloudferry/internal/yandex"
)

//go:embed all:web
var webAssets embed.FS

// sevenDigits matches the confirmation code Yandex shows in the browser.
var sevenDigits = regexp.MustCompile(`^\d{7}$`)

// Engine is the rclone surface used by the HTTP layer. It is an interface so
// handlers can be tested without a running rclone.
type Engine interface {
	Call(ctx context.Context, method string, in, out any) error
	Ping(ctx context.Context) (rc.Version, error)
}

// Config holds server wiring.
type Config struct {
	Store     *store.Store
	Rclone    Engine
	Jobs      *jobs.Manager
	Downloads string   // default destination root, e.g. /downloads
	DestRoots []string // allowed destination roots
	Version   string
	BasicUser string // optional HTTP basic auth for standalone installs
	BasicPass string
}

// Server serves the UI and the JSON API.
type Server struct {
	cfg Config
	mux *http.ServeMux

	flowsMu sync.Mutex
	flows   map[string]*deviceFlow

	cloud   *cloudInformer
	folders *cloudFoldersCache
}

// New builds the HTTP handler.
func New(cfg Config) (*Server, error) {
	if cfg.Downloads == "" {
		cfg.Downloads = "/downloads"
	}
	if len(cfg.DestRoots) == 0 {
		cfg.DestRoots = []string{cfg.Downloads}
	}
	s := &Server{
		cfg:     cfg,
		mux:     http.NewServeMux(),
		flows:   map[string]*deviceFlow{},
		cloud:   newCloudInformer(cfg.Store, cfg.Rclone, time.Now),
		folders: newCloudFoldersCache(time.Now),
	}
	if err := s.routes(); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the root handler with security headers and a JSON-only
// guard for mutating requests.
func (s *Server) Handler() http.Handler {
	var handler http.Handler = securityHeaders(requireJSON(s.mux))
	if s.cfg.BasicUser != "" {
		handler = basicAuth(s.cfg.BasicUser, s.cfg.BasicPass, handler)
	}
	return handler
}

// basicAuth protects a standalone install that is reachable over the LAN
// without Umbrel's gateway in front.
func basicAuth(user, pass string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(gotUser), []byte(user)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(gotPass), []byte(pass)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="Cloud Ferry", charset="UTF-8"`)
			writeError(w, http.StatusUnauthorized, "Нужен вход в приложение.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

// requireJSON rejects mutating requests that are not JSON. It also blocks the
// simplest cross-site form posts, as a defence-in-depth layer behind Umbrel's
// gateway auth.
func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			contentType := r.Header.Get("Content-Type")
			if !strings.HasPrefix(contentType, "application/json") {
				writeError(w, http.StatusUnsupportedMediaType, "Ожидается JSON.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) routes() error {
	sub, err := fs.Sub(webAssets, "web")
	if err != nil {
		return fmt.Errorf("embedded web assets: %w", err)
	}
	s.mux.HandleFunc("GET /api/healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/readyz", s.handleReady)
	s.mux.HandleFunc("GET /api/widget", s.handleWidget)
	s.mux.HandleFunc("GET /api/state", s.handleState)
	s.mux.HandleFunc("GET /api/system", s.handleSystem)
	s.mux.HandleFunc("GET /api/fs/dirs", s.handleFSDirs)
	s.mux.HandleFunc("GET /api/fs/folders", s.handleFSDirs)
	s.mux.HandleFunc("POST /api/fs/dirs", s.handleFSCreateDir)
	s.mux.HandleFunc("POST /api/accounts", s.handleCreateAccount)
	s.mux.HandleFunc("POST /api/accounts/validate", s.handleValidateAccount)
	s.mux.HandleFunc("GET /api/accounts/validate", s.handleValidateAccountMethodGuard)
	s.mux.HandleFunc("POST /api/oauth/yandex/start", s.handleYandexAuthURL)
	s.mux.HandleFunc("POST /api/oauth/yandex/finish", s.handleYandexFinish)
	s.mux.HandleFunc("POST /api/oauth/yandex/device/start", s.handleDeviceStart)
	s.mux.HandleFunc("GET /api/oauth/yandex/device/{id}", s.handleDeviceStatus)
	s.mux.HandleFunc("DELETE /api/oauth/yandex/device/{id}", s.handleDeviceCancel)
	s.mux.HandleFunc("DELETE /api/accounts/{id}", s.handleDeleteAccount)
	s.mux.HandleFunc("POST /api/accounts/{id}/probe", s.handleProbe)
	s.mux.HandleFunc("GET /api/accounts/{id}/folders", s.handleCloudFolders)
	s.mux.HandleFunc("POST /api/preflight", s.handlePreflight)
	s.mux.HandleFunc("POST /api/jobs/preflight", s.handlePreflight)
	s.mux.HandleFunc("POST /api/jobs", s.handleCreateJob)
	s.mux.HandleFunc("POST /api/jobs/{id}/stop", s.handleStopJob)
	s.mux.HandleFunc("POST /api/jobs/{id}/resume", s.handleResumeJob)
	s.mux.HandleFunc("POST /api/jobs/{id}/check", s.handleCheckJob)
	s.mux.Handle("GET /", staticHandler(sub))
	return nil
}

func staticHandler(sub fs.FS) http.Handler {
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.ServeFileFS(w, r, sub, "index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}

// --- handlers ---------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"ok": true, "version": s.cfg.Version}
	ctx, cancel := contextWithTimeout(r, 4*time.Second)
	defer cancel()
	if v, err := s.cfg.Rclone.Ping(ctx); err != nil {
		resp["ok"] = false
		resp["rclone"] = nil
		resp["error"] = err.Error()
	} else {
		resp["rclone"] = v.Version
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleReady is the readiness probe: 200 only when the engine answers rc.
// /api/healthz stays a liveness check that always returns 200 with "ok": false
// when rclone is down, so a restarting engine does not flap the web server;
// /api/readyz uses the HTTP status for orchestration and uptime checks.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 4*time.Second)
	defer cancel()
	version, err := s.cfg.Rclone.Ping(ctx)
	if err != nil {
		log.Printf("readyz: rclone is not ready: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok":      false,
			"version": s.cfg.Version,
			"error":   "rclone недоступен",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": s.cfg.Version,
		"rclone":  version.Version,
	})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	accounts, _ := s.cfg.Store.Snapshot()
	jobList := s.cfg.Store.Jobs()
	writeJSON(w, http.StatusOK, map[string]any{
		"accounts":  accounts,
		"jobs":      jobList,
		"downloads": s.cfg.Downloads,
		"roots":     s.cfg.DestRoots,
	})
}

type createAccountRequest struct {
	Name         string `json:"name"`
	Provider     string `json:"provider"` // webdav | yandex-token
	URL          string `json:"url"`
	User         string `json:"user"`
	Password     string `json:"password"`
	Token        string `json:"token"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := decodeJSON(r, &req); err != nil {
		s.badRequest(w, err)
		return
	}
	req.Name = limitName(req.Name)
	if req.Name == "" {
		req.Name = "Облако"
	}

	id, err := newID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	remote := "cf_" + id

	params, opt, login, err := accountRemoteParams(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	call := map[string]any{"name": remote, "type": providerType(req.Provider), "parameters": params, "opt": opt}
	if err := s.cfg.Rclone.Call(ctx, "config/create", call, nil); err != nil {
		s.fail(w, http.StatusBadGateway, "Не удалось подключиться к облаку. Проверьте адрес, логин и пароль.", err)
		return
	}

	account := store.Account{
		ID:        id,
		Name:      req.Name,
		Provider:  req.Provider,
		Remote:    remote,
		CreatedAt: time.Now().UTC(),
	}
	// For WebDAV the login is the only human identity the provider gives us,
	// so it becomes the account name the UI shows. Token and OAuth flows keep
	// the name the client passed.
	if login != "" {
		account.Login = login
		account.Name = login
	}
	if err := s.cfg.Store.AddAccount(account); err != nil {
		s.rollbackRemote(remote)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

// accountRemoteParams maps the account form to rclone's config/create
// arguments. Creating a real account and validating a temporary one must send
// the same parameters, so both go through this single mapping.
func accountRemoteParams(req createAccountRequest) (params, opt map[string]any, login string, err error) {
	switch req.Provider {
	case "webdav":
		user := strings.TrimSpace(req.User)
		if req.URL == "" || user == "" || req.Password == "" {
			return nil, nil, "", errors.New("Заполните адрес, логин и пароль приложения.")
		}
		params = map[string]any{
			"url":    req.URL,
			"vendor": "other",
			"user":   user,
			"pass":   req.Password,
		}
		opt = map[string]any{"nonInteractive": true, "obscure": true}
		return params, opt, limitName(user), nil
	case "yandex-token":
		token := strings.TrimSpace(req.Token)
		if token == "" {
			return nil, nil, "", errors.New("Вставьте токен доступа.")
		}
		normalized, err := normalizeTokenValue(token)
		if err != nil {
			return nil, nil, "", err
		}
		params = map[string]any{"token": normalized}
		if clientID := strings.TrimSpace(req.ClientID); clientID != "" {
			params["client_id"] = clientID
		}
		if secret := strings.TrimSpace(req.ClientSecret); secret != "" {
			params["client_secret"] = secret
		}
		return params, map[string]any{"nonInteractive": true}, "", nil
	default:
		return nil, nil, "", errors.New("Этот способ подключения пока не поддерживается.")
	}
}

// limitName trims and caps a user-supplied cloud name so it cannot break the
// layout or the state file.
func limitName(name string) string {
	name = strings.TrimSpace(name)
	runes := []rune(name)
	if len(runes) > 64 {
		name = strings.TrimSpace(string(runes[:64]))
	}
	return name
}

// rollbackRemote removes a remote that was created but never recorded, so
// rclone.conf does not collect orphans.
func (s *Server) rollbackRemote(remote string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.cfg.Rclone.Call(ctx, "config/delete", map[string]any{"name": remote}, nil); err != nil {
		log.Printf("rollback %s: %v", remote, err)
	}
}

type oauthStartRequest struct {
	ClientID string `json:"clientId"`
}

func (s *Server) handleYandexAuthURL(w http.ResponseWriter, r *http.Request) {
	var req oauthStartRequest
	if err := decodeJSON(r, &req); err != nil {
		s.badRequest(w, err)
		return
	}
	if strings.TrimSpace(req.ClientID) == "" {
		writeError(w, http.StatusBadRequest, "Укажите ID приложения.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": yandex.AuthURL(req.ClientID)})
}

type oauthFinishRequest struct {
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Code         string `json:"code"`
}

func (s *Server) handleYandexFinish(w http.ResponseWriter, r *http.Request) {
	var req oauthFinishRequest
	if err := decodeJSON(r, &req); err != nil {
		s.badRequest(w, err)
		return
	}
	code := strings.TrimSpace(req.Code)
	if strings.TrimSpace(req.ClientID) == "" || code == "" {
		writeError(w, http.StatusBadRequest, "Укажите ID приложения и код из браузера.")
		return
	}
	if !sevenDigits.MatchString(code) {
		writeError(w, http.StatusBadRequest, "Код из браузера состоит из семи цифр.")
		return
	}

	exchangeCtx, exchangeCancel := contextWithTimeout(r, 40*time.Second)
	defer exchangeCancel()
	token, err := yandex.Exchange(exchangeCtx, req.ClientID, req.ClientSecret, code)
	if err != nil {
		s.fail(w, http.StatusBadGateway, oauthMessage(err), err)
		return
	}

	createCtx, createCancel := contextWithTimeout(r, 30*time.Second)
	defer createCancel()
	account, err := s.createYandexAccount(createCtx, req.Name, strings.TrimSpace(req.ClientID), req.ClientSecret, token)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "Не удалось сохранить подключение. Попробуйте ещё раз.", err)
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func providerType(provider string) string {
	if provider == "yandex-token" {
		return "yandex"
	}
	return "webdav"
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	account, ok := s.cfg.Store.Account(id)
	if !ok {
		writeError(w, http.StatusNotFound, "Подключение не найдено. Возможно, оно уже отключено.")
		return
	}
	for _, job := range s.cfg.Store.Jobs() {
		if job.AccountID == id && job.Status == store.StatusRunning {
			writeError(w, http.StatusConflict, "Сначала остановите скачивание из этого облака.")
			return
		}
	}

	removed, err := s.cfg.Store.DeleteAccount(id)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Не удалось отключить облако. Попробуйте ещё раз.", err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "Подключение не найдено. Возможно, оно уже отключено.")
		return
	}

	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()
	if err := s.cfg.Rclone.Call(ctx, "config/delete", map[string]any{"name": account.Remote}, nil); err != nil {
		log.Printf("config/delete %s: %v", account.Remote, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	account, ok := s.cfg.Store.Account(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Подключение не найдено. Возможно, оно уже отключено.")
		return
	}

	ctx, cancel := contextWithTimeout(r, 40*time.Second)
	defer cancel()

	var about struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
		Free  int64 `json:"free"`
	}
	if err := s.cfg.Rclone.Call(ctx, "operations/about", map[string]any{"fs": account.Remote + ":"}, &about); err != nil {
		if health.IsAuthError(err.Error()) {
			_ = s.cfg.Store.UpdateAccount(account.ID, func(a *store.Account) bool {
				a.Status = store.AccountStatusAuth
				a.StatusNote = "Нужно подключить облако заново"
				a.CheckedAt = time.Now().UTC()
				return true
			})
		}
		s.fail(w, http.StatusBadGateway, "Не удалось узнать занятое место в облаке. Попробуйте ещё раз.", err)
		return
	}
	_ = s.cfg.Store.UpdateAccount(account.ID, func(a *store.Account) bool {
		a.Status = store.AccountStatusOK
		a.StatusNote = ""
		a.CheckedAt = time.Now().UTC()
		return true
	})

	var listing struct {
		List []struct {
			Path  string `json:"Path"`
			Name  string `json:"Name"`
			IsDir bool   `json:"IsDir"`
			Size  int64  `json:"Size"`
		} `json:"list"`
	}
	listCtx, listCancel := contextWithTimeout(r, 40*time.Second)
	defer listCancel()
	warning := ""
	if err := s.cfg.Rclone.Call(listCtx, "operations/list", map[string]any{
		"fs":     account.Remote + ":",
		"remote": "",
		"opt":    map[string]any{"dirsOnly": true},
	}, &listing); err != nil {
		warning = "Не удалось прочитать список папок. Попробуйте проверить подключение ещё раз."
	}

	names := make([]string, 0, len(listing.List))
	for _, item := range listing.List {
		names = append(names, item.Name)
	}

	loginCtx, loginCancel := contextWithTimeout(r, 10*time.Second)
	defer loginCancel()
	login := s.accountLogin(loginCtx, account)

	writeJSON(w, http.StatusOK, map[string]any{
		"about":     about,
		"dirs":      names,
		"warning":   warning,
		"knownUser": login != "",
		"login":     login,
	})
}

// accountLogin reports the login shown in the UI: the WebDAV user name. New
// accounts store it at creation; for accounts saved by older versions rclone's
// own config is the only source. Providers without a login (Yandex OAuth and
// tokens) return an empty string instead of a guessed value.
func (s *Server) accountLogin(ctx context.Context, account store.Account) string {
	if account.Login != "" {
		return account.Login
	}
	if account.Provider != "webdav" {
		return ""
	}
	var stored struct {
		User string `json:"user"`
	}
	if err := s.cfg.Rclone.Call(ctx, "config/get", map[string]any{"name": account.Remote}, &stored); err != nil {
		return ""
	}
	return strings.TrimSpace(stored.User)
}

type preflightRequest struct {
	AccountID   string    `json:"accountId"`
	SourcePath  string    `json:"sourcePath"`
	SourcePaths *[]string `json:"sourcePaths"`
	DestPath    string    `json:"destPath"`
}

// handlePreflight counts files and bytes in the cloud before a transfer starts.
// It can take a while on large accounts: rclone has to list the tree.
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	var req preflightRequest
	if err := decodeJSON(r, &req); err != nil {
		s.badRequest(w, err)
		return
	}
	account, ok := s.cfg.Store.Account(req.AccountID)
	if !ok {
		writeError(w, http.StatusNotFound, "Подключение не найдено. Возможно, оно уже отключено.")
		return
	}
	src, err := parseSources(req.SourcePath, req.SourcePaths)
	if err != nil {
		s.sourceError(w, err)
		return
	}
	dst, err := s.cleanDestPath(req.DestPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fsPath, filter := jobs.SourceFS(account.Remote, src)
	sizeReq := map[string]any{"fs": fsPath}
	if filter != nil {
		sizeReq["_filter"] = filter
	}
	var size struct {
		Count int64 `json:"count"`
		Bytes int64 `json:"bytes"`
	}
	ctx, cancel := contextWithTimeout(r, 10*time.Minute)
	defer cancel()
	if err := s.cfg.Rclone.Call(ctx, "operations/size", sizeReq, &size); err != nil {
		s.fail(w, http.StatusBadGateway, "Не удалось посчитать объём. Попробуйте ещё раз.", err)
		return
	}

	var free uint64
	if value, err := disk.Free(dst); err == nil {
		free = value
	}

	// rclone stops at 90% of the free space (see transferLimit), so the
	// preflight must use the same threshold: a transfer that fits into 100% of
	// the free space but not into 90% would stop halfway. free == 0 means the
	// filesystem could not be measured: the fit is unknown, so do not block
	// the user.
	fits := true
	if free > 0 && size.Bytes > 0 {
		fits = uint64(size.Bytes) <= disk.TransferLimit(free)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"files": size.Count,
		"bytes": size.Bytes,
		"free":  free,
		"fits":  fits,
	})
}

type createJobRequest struct {
	AccountID   string    `json:"accountId"`
	SourcePath  string    `json:"sourcePath"`
	SourcePaths *[]string `json:"sourcePaths"`
	DestPath    string    `json:"destPath"`
	Mode        string    `json:"mode"`
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req createJobRequest
	if err := decodeJSON(r, &req); err != nil {
		s.badRequest(w, err)
		return
	}
	account, ok := s.cfg.Store.Account(req.AccountID)
	if !ok {
		writeError(w, http.StatusNotFound, "Подключение не найдено. Возможно, оно уже отключено.")
		return
	}

	src, err := parseSources(req.SourcePath, req.SourcePaths)
	if err != nil {
		s.sourceError(w, err)
		return
	}
	dst, err := s.cleanDestPath(req.DestPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := req.Mode
	if mode == "" {
		mode = "copy"
	}
	if mode != "copy" && mode != "sync" {
		writeError(w, http.StatusBadRequest, "Выберите режим «Добавлять» или «Как в облаке».")
		return
	}
	if mode == "sync" && path.Clean(dst) == path.Clean(s.cfg.Downloads) {
		writeError(w, http.StatusBadRequest, "Для режима «Как в облаке» выберите подпапку, а не общую папку загрузок.")
		return
	}

	ctx, cancel := contextWithTimeout(r, 90*time.Second)
	defer cancel()
	job, err := s.cfg.Jobs.Start(ctx, account, src, dst, mode)
	switch {
	case errors.Is(err, jobs.ErrConflict):
		writeError(w, http.StatusConflict, "По этому пути уже идёт скачивание. Дождитесь окончания или остановите его.")
		return
	case errors.Is(err, jobs.ErrInvalidMode):
		writeError(w, http.StatusBadRequest, "Выберите режим «Добавлять» или «Как в облаке».")
		return
	case errors.Is(err, jobs.ErrSourceNotFound):
		sourceNotFound(w, sourceField(src), err)
		return
	case err != nil:
		s.fail(w, http.StatusBadGateway, "Не удалось начать скачивание. Попробуйте ещё раз.", err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleStopJob(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.cfg.Store.Job(r.PathValue("id")); !ok {
		writeError(w, http.StatusNotFound, "Скачивание не найдено. Обновите страницу.")
		return
	}
	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	warning, err := s.cfg.Jobs.Stop(ctx, r.PathValue("id"))
	if errors.Is(err, jobs.ErrConflict) {
		writeError(w, http.StatusConflict, "Это скачивание уже завершено — останавливать нечего.")
		return
	}
	if err != nil {
		s.fail(w, http.StatusBadGateway, "Не удалось остановить скачивание. Обновите страницу и попробуйте снова.", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": r.PathValue("id"), "warning": warning})
}

func (s *Server) handleResumeJob(w http.ResponseWriter, r *http.Request) {
	stored, ok := s.cfg.Store.Job(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Скачивание не найдено. Обновите страницу.")
		return
	}
	// The saved destination may have become unavailable or outside the allowed
	// roots since the job was created (roots can change between restarts).
	// Validate it before the job is claimed or rclone is launched.
	if _, err := s.cleanDestPath(stored.Dest); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := contextWithTimeout(r, 90*time.Second)
	defer cancel()
	job, err := s.cfg.Jobs.Resume(ctx, r.PathValue("id"))
	if errors.Is(err, jobs.ErrConflict) {
		writeError(w, http.StatusConflict, "Это скачивание нельзя продолжить: оно уже идёт или завершено.")
		return
	}
	if errors.Is(err, jobs.ErrSourceNotFound) {
		sourceNotFound(w, sourceFieldForPaths(stored.SourcePaths), err)
		return
	}
	if err != nil {
		s.fail(w, http.StatusBadGateway, "Не удалось продолжить скачивание. Возможно, облако отключено — подключите его заново.", err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleCheckJob(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.cfg.Store.Job(r.PathValue("id")); !ok {
		writeError(w, http.StatusNotFound, "Скачивание не найдено. Обновите страницу.")
		return
	}
	ctx, cancel := contextWithTimeout(r, 2*time.Hour)
	defer cancel()
	result, err := s.cfg.Jobs.Check(ctx, r.PathValue("id"))
	if err != nil {
		s.fail(w, http.StatusBadGateway, "Не удалось проверить файлы. Попробуйте ещё раз.", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleWidget(w http.ResponseWriter, r *http.Request) {
	_, jobList := s.cfg.Store.Snapshot()
	widget := map[string]any{
		"type":          "text-with-progress",
		"refresh":       "5s",
		"link":          "",
		"title":         "Cloud Ferry",
		"text":          "Нет активных скачиваний",
		"progressLabel": "",
		"progress":      0,
	}
	for _, job := range jobList {
		if job.Status != store.StatusRunning {
			continue
		}
		progress := 0.0
		if job.Stats.TotalBytes > 0 {
			progress = float64(job.Stats.Bytes) / float64(job.Stats.TotalBytes)
			if progress > 1 {
				progress = 1
			}
		}
		widget["text"] = fmt.Sprintf("%s из %s · %s/с",
			humanBytes(job.Stats.Bytes), humanBytes(job.Stats.TotalBytes), humanBytes(int64(job.Stats.Speed)))
		widget["progressLabel"] = "Скачивание"
		widget["progress"] = progress
		break
	}
	writeJSON(w, http.StatusOK, widget)
}

// --- validation -------------------------------------------------------------

func cleanSourcePath(p string) (string, error) {
	p = strings.TrimSpace(strings.Trim(p, "/"))
	if p == "" {
		return "", nil
	}
	cleaned := path.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", errors.New("Проверьте путь в облаке.")
	}
	if strings.HasPrefix(cleaned, "/") {
		return "", errors.New("Укажите папку в облаке без косой черты в начале.")
	}
	return cleaned, nil
}

func (s *Server) cleanDestPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		p = s.cfg.Downloads
	}
	cleaned, err := fsbrowse.ValidateDest(s.cfg.DestRoots, p)
	if err != nil {
		return "", s.destError(err)
	}
	return cleaned, nil
}

// destError turns a validation sentinel into a short user sentence.
func (s *Server) destError(err error) error {
	switch {
	case errors.Is(err, fsbrowse.ErrInvalidPath):
		return errors.New("Укажите полный путь на сервере, например /downloads.")
	case errors.Is(err, fsbrowse.ErrOutsideRoots):
		return fmt.Errorf("В эту папку сохранять нельзя. Доступные папки: %s.", strings.Join(s.cfg.DestRoots, ", "))
	case errors.Is(err, fsbrowse.ErrSymlink):
		return errors.New("Эта папка ведёт за пределы доступной. Выберите другую.")
	case errors.Is(err, fsbrowse.ErrNotDir):
		return errors.New("Это файл, а не папка.")
	default:
		return errors.New("Не удалось проверить папку на сервере. Попробуйте ещё раз.")
	}
}

// --- helpers ----------------------------------------------------------------

// ErrBodyTooLarge is returned for requests above the size limit.
var ErrBodyTooLarge = errors.New("body too large")

func decodeJSON(r *http.Request, dst any) error {
	const maxBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxBody {
		return ErrBodyTooLarge
	}
	if len(body) == 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("bad json: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("http: write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail logs the technical cause and shows a human sentence to the user.
func (s *Server) fail(w http.ResponseWriter, status int, msg string, err error) {
	if err != nil {
		log.Printf("%s: %v", msg, err)
	}
	writeError(w, status, msg)
}

func (s *Server) badRequest(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrBodyTooLarge) {
		s.fail(w, http.StatusRequestEntityTooLarge, "Запрос слишком большой.", err)
		return
	}
	s.fail(w, http.StatusBadRequest, "Не удалось прочитать запрос. Обновите страницу и попробуйте снова.", err)
}

func oauthMessage(err error) string {
	var oauthErr *yandex.OAuthError
	if errors.As(err, &oauthErr) {
		switch oauthErr.Code {
		case "invalid_grant", "expired_token":
			return "Код не подошёл или уже истёк. Получите новый код и попробуйте снова."
		case "invalid_client", "invalid_request":
			return "ID или секрет приложения не подошли. Проверьте их в настройках приложения на oauth.yandex.ru."
		case "unauthorized_client":
			return "У приложения не включён вход через Яндекс ID. Включите его в настройках приложения."
		}
	}
	return "Яндекс не принял код. Получите новый код и попробуйте снова."
}

func newID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	// A string of Cyrillic letters cannot be indexed by rune: units[exp] on a
	// plain string reads one byte and prints garbage like "Ð". Keep a slice.
	units := []string{"К", "М", "Г", "Т", "П", "Э"}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < len(units)-1; v /= unit {
		div *= unit
		exp++
	}
	return strings.Replace(fmt.Sprintf("%.1f %sБ", float64(n)/float64(div), units[exp]), ".", ",", 1)
}
