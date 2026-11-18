package httpapi

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"cloudferry/internal/health"
	"cloudferry/internal/rc"
)

// validateTimeout bounds the whole temporary-remote check: create the remote,
// ask for quota and list the root. Cleanup has its own deadline, so a slow
// provider cannot leave a remote behind.
var validateTimeout = 18 * time.Second

// maxValidateCleanupTimeout bounds config/delete after the check is over. It is
// independent from the request: the client may already be gone.
var maxValidateCleanupTimeout = 10 * time.Second

const (
	maxValidateURLLen      = 2048
	maxValidateUserLen     = 256
	maxValidatePasswordLen = 4096
	maxValidateTokenLen    = 16 << 10
)

// validateAbout is the quota a successful check reports, shaped like the
// subset of operations/about the UI shows.
type validateAbout struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

// validateFieldError names the form field that must be highlighted.
type validateFieldError struct {
	field   string
	message string
}

func (e *validateFieldError) Error() string { return e.message }

// validateFailure is one classified reason a check did not pass. The message is
// user-facing: it never contains rclone internals, which only go to the log.
type validateFailure struct {
	status  int
	code    string
	message string
	field   string
	stage   string
	cause   error
}

// handleValidateAccountMethodGuard keeps the check endpoint a pure POST: a GET
// must not fall through to the static file handler. Other methods are rejected
// by ServeMux itself, because only POST has a pattern for this path.
func (s *Server) handleValidateAccountMethodGuard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", http.MethodPost)
	writeValidateError(w, http.StatusMethodNotAllowed, "invalid_request", "", "Этот адрес принимает только POST.")
}

// handleValidateAccount checks that the credentials work without creating an
// account. It creates a throwaway rclone remote, asks the provider for quota
// and a root listing, then always deletes the remote. The store is untouched.
func (s *Server) handleValidateAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := decodeJSON(r, &req); err != nil {
		if errors.Is(err, ErrBodyTooLarge) {
			writeValidateError(w, http.StatusRequestEntityTooLarge, "invalid_request", "",
				"Запрос слишком большой.")
			return
		}
		log.Printf("validate account: bad request: %v", err)
		writeValidateError(w, http.StatusBadRequest, "invalid_request", "",
			"Не удалось прочитать запрос. Обновите страницу и попробуйте снова.")
		return
	}
	if fieldErr := validateAccountInput(&req); fieldErr != nil {
		writeValidateError(w, http.StatusBadRequest, "invalid_request", fieldErr.field, fieldErr.message)
		return
	}
	params, opt, login, err := accountRemoteParams(req)
	if err != nil {
		// validateAccountInput already covers every rejection; keep the safety
		// net so a future provider cannot silently skip the form checks.
		writeValidateError(w, http.StatusBadRequest, "invalid_request", "provider", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), validateTimeout)
	defer cancel()

	about, failure := s.checkTemporaryRemote(ctx, req.Provider, params, opt)
	if failure != nil {
		log.Printf("validate account: %s (%s): %s", failure.code, failure.stage, redactSecrets(failure.cause, req))
		writeValidateFailure(w, failure)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"about": about,
		"login": login,
	})
}

// checkTemporaryRemote runs the rc calls on a remote whose name only the
// server knows, and removes it on every return path.
func (s *Server) checkTemporaryRemote(ctx context.Context, provider string, params, opt map[string]any) (validateAbout, *validateFailure) {
	id, err := newID()
	if err != nil {
		return validateAbout{}, &validateFailure{
			status:  http.StatusInternalServerError,
			code:    "internal_error",
			message: "Не удалось проверить подключение. Попробуйте ещё раз.",
			stage:   "name",
			cause:   err,
		}
	}
	name := "cfcheck_" + id

	created := false
	defer func() {
		if created {
			s.deleteTemporaryRemote(name)
		}
	}()

	call := map[string]any{
		"name":       name,
		"type":       providerType(provider),
		"parameters": params,
		"opt":        opt,
	}
	if err := s.cfg.Rclone.Call(ctx, "config/create", call, nil); err != nil {
		return validateAbout{}, classifyValidateFailure(provider, "config/create", err)
	}
	created = true

	var about validateAbout
	if err := s.cfg.Rclone.Call(ctx, "operations/about", map[string]any{"fs": name + ":"}, &about); err != nil {
		return validateAbout{}, classifyValidateFailure(provider, "operations/about", err)
	}

	// Quota can be reported by a provider that cannot list the disk; reading
	// the root proves the account is actually usable.
	var listing struct {
		List []struct {
			Name  string `json:"Name"`
			IsDir bool   `json:"IsDir"`
		} `json:"list"`
	}
	listReq := map[string]any{
		"fs":     name + ":",
		"remote": "",
		"opt":    map[string]any{"dirsOnly": true},
	}
	if err := s.cfg.Rclone.Call(ctx, "operations/list", listReq, &listing); err != nil {
		return validateAbout{}, classifyValidateFailure(provider, "operations/list", err)
	}
	return about, nil
}

// deleteTemporaryRemote removes a throwaway remote even when the request is
// already over or cancelled.
func (s *Server) deleteTemporaryRemote(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), maxValidateCleanupTimeout)
	defer cancel()
	if err := s.cfg.Rclone.Call(ctx, "config/delete", map[string]any{"name": name}, nil); err != nil {
		log.Printf("validate: delete temporary remote %s: %v", name, err)
	}
}

// validateAccountInput checks the form before any rclone call. It normalises
// the fields the same way accountRemoteParams does, but reports which field the
// UI should highlight.
func validateAccountInput(req *createAccountRequest) *validateFieldError {
	req.URL = strings.TrimSpace(req.URL)
	req.User = strings.TrimSpace(req.User)
	req.Token = strings.TrimSpace(req.Token)
	req.ClientID = strings.TrimSpace(req.ClientID)
	req.ClientSecret = strings.TrimSpace(req.ClientSecret)

	switch req.Provider {
	case "webdav":
		if req.URL == "" {
			return &validateFieldError{"url", "Укажите адрес сервера WebDAV, например https://webdav.yandex.ru."}
		}
		if len(req.URL) > maxValidateURLLen {
			return &validateFieldError{"url", "Адрес сервера слишком длинный."}
		}
		parsed, err := url.Parse(req.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return &validateFieldError{"url", "Проверьте адрес сервера: нужен полный адрес вида https://webdav.yandex.ru."}
		}
		if isLocalHost(parsed.Hostname()) {
			return &validateFieldError{"url", "Укажите публичный адрес сервера: адреса локальной сети и самого сервера не поддерживаются."}
		}
		if !allowedWebDAVHost(parsed.Hostname()) {
			return &validateFieldError{"url", "Этот сервер WebDAV не поддерживается. Укажите адрес Яндекс Диска."}
		}
		if req.User == "" {
			return &validateFieldError{"user", "Укажите логин от Яндекс Диска."}
		}
		if len(req.User) > maxValidateUserLen {
			return &validateFieldError{"user", "Логин слишком длинный."}
		}
		if req.Password == "" {
			return &validateFieldError{"password", "Укажите пароль приложения."}
		}
		if len(req.Password) > maxValidatePasswordLen {
			return &validateFieldError{"password", "Пароль приложения слишком длинный."}
		}
	case "yandex-token":
		if req.Token == "" {
			return &validateFieldError{"token", "Вставьте токен доступа."}
		}
		if len(req.Token) > maxValidateTokenLen {
			return &validateFieldError{"token", "Токен слишком длинный."}
		}
		// A raw OAuth token is accepted as well: it is wrapped into the JSON shape
		// rclone expects when the account is created, see normalizeTokenValue.
		if _, err := normalizeTokenValue(req.Token); err != nil {
			return &validateFieldError{"token", err.Error()}
		}
		if len(req.ClientID) > maxValidateUserLen {
			return &validateFieldError{"clientId", "ID приложения слишком длинный."}
		}
		if len(req.ClientSecret) > maxValidateUserLen {
			return &validateFieldError{"clientSecret", "Секрет приложения слишком длинный."}
		}
	default:
		return &validateFieldError{"provider", "Этот способ подключения пока не поддерживается."}
	}
	return nil
}

// isLocalHost reports whether a WebDAV host points at the machine itself, its
// loopback or a local network. Cloud Ferry runs on a home server, so accepting
// such an address would let the account form make the server reach its own
// internal network (SSRF). The check deliberately looks only at literal IPs and
// localhost names: resolving a name during validation would both slow the form
// down and still be racy (DNS can answer differently when rclone connects
// later), so a public-looking name that resolves to a private address remains a
// residual risk. The account form targets public cloud providers, not LAN boxes.
// defaultWebDAVHosts is the allowlist of WebDAV servers Cloud Ferry talks to.
// The address is fixed in the UI, so the list only needs extending when Yandex
// changes its endpoint or a custom server is enabled on purpose.
var defaultWebDAVHosts = []string{"webdav.yandex.ru"}

// allowedWebDAVHost checks the host against the built-in list plus the optional
// CF_WEBDAV_HOSTS override (comma-separated), so a new endpoint does not
// require a rebuild.
func allowedWebDAVHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, allowed := range defaultWebDAVHosts {
		if host == allowed {
			return true
		}
	}
	for _, extra := range strings.Split(os.Getenv("CF_WEBDAV_HOSTS"), ",") {
		extra = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(extra, ".")))
		if extra != "" && host == extra {
			return true
		}
	}
	return false
}

func isLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	// Зона интерфейса (%eth0) не является частью адреса и мешает разбору:
	// без её отбрасывания link-local fe80::1%eth0 считался публичным хостом.
	if i := strings.LastIndexByte(host, '%'); i > 0 {
		host = host[:i]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Числовая форма IPv4 (http://2130706433) равнозначна 127.0.0.1.
		if n, err := strconv.ParseUint(host, 10, 32); err == nil && host != "" {
			ip = net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		}
	}
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}

// classifyValidateFailure turns an rclone error into the agreed HTTP answer.
// Order matters: an unenabled WebDAV may also answer 403, so "WebDAV is
// disabled" wins over the plain auth branch.
func classifyValidateFailure(provider, stage string, err error) *validateFailure {
	message := strings.ToLower(err.Error())
	apiStatus := 0
	var apiErr *rc.APIError
	if errors.As(err, &apiErr) {
		apiStatus = apiErr.Status
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		apiStatus == http.StatusGatewayTimeout || apiStatus == http.StatusRequestTimeout ||
		containsAny(message, "context deadline exceeded", "i/o timeout", "timed out", "timeout") {
		return &validateFailure{
			status:  http.StatusGatewayTimeout,
			code:    "timeout",
			message: "Проверка заняла слишком много времени. Попробуйте ещё раз.",
			stage:   stage,
			cause:   err,
		}
	}

	if provider == "webdav" && isWebDAVUnavailable(message, apiStatus) {
		return &validateFailure{
			status:  http.StatusBadRequest,
			code:    "webdav_unavailable",
			message: "Вход по WebDAV выключен или недоступен. Включите WebDAV в настройках Яндекс Диска (обычно нужна подписка Яндекс 360) и попробуйте снова.",
			stage:   stage,
			cause:   err,
		}
	}

	// rclone refuses a token that has no refresh token: such a token cannot be
	// renewed, so it reports "unable to get RefreshToken" instead of a 401.
	if containsAny(message, "unable to get refreshtoken", "no refresh token", "refresh token is empty") {
		return &validateFailure{
			status:  http.StatusBadRequest,
			code:    "auth_failed",
			message: "Токен без возможности обновления. Вставьте полный JSON: access_token, refresh_token и expiry.",
			field:   "token",
			stage:   stage,
			cause:   err,
		}
	}

	if health.IsAuthError(message) || (provider == "yandex-token" && isTokenRejected(message)) {
		failure := &validateFailure{
			status:  http.StatusBadRequest,
			code:    "auth_failed",
			message: "Яндекс Диск не принял данные для входа. Проверьте логин и пароль приложения.",
			stage:   stage,
			cause:   err,
		}
		if provider == "yandex-token" {
			failure.message = "Яндекс Диск не принял токен. Получите новый токен или войдите заново."
			failure.field = "token"
		} else {
			failure.field = "password"
		}
		return failure
	}

	return &validateFailure{
		status:  http.StatusBadGateway,
		code:    "upstream_error",
		message: "Проверка сейчас недоступна: сервис не отвечает. Попробуйте ещё раз через минуту.",
		stage:   stage,
		cause:   err,
	}
}

// isWebDAVUnavailable recognises an account or server without a working WebDAV
// endpoint, as opposed to wrong credentials.
func isWebDAVUnavailable(message string, status int) bool {
	switch status {
	case http.StatusMethodNotAllowed, http.StatusNotImplemented,
		http.StatusBadGateway, http.StatusServiceUnavailable:
		return true
	}
	return containsAny(message,
		"405", "501", "502", "503",
		"method not allowed", "not implemented", "bad gateway", "service unavailable",
		"webdav is not enabled", "webdav is disabled", "webdav disabled",
		"webdav is not available", "webdav not available",
		"webdav is not supported", "webdav not supported",
		"webdav access is disabled", "webdav access disabled",
	)
}

// isTokenRejected recognises the token errors rclone's yandex backend gets
// without an HTTP 401, so they are not reported as an upstream outage.
func isTokenRejected(message string) bool {
	return containsAny(message, "invalid_grant", "invalid_token", "expired_token", "token expired")
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

// redactSecrets makes sure a logged rclone error cannot echo credentials even
// if a provider or transport puts them into the error text.
func redactSecrets(err error, req createAccountRequest) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	for _, secret := range []string{req.Password, req.Token, req.ClientSecret} {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}

func writeValidateError(w http.ResponseWriter, status int, code, field, message string) {
	payload := map[string]any{"error": message, "code": code}
	if field != "" {
		payload["field"] = field
	}
	writeJSON(w, status, payload)
}

func writeValidateFailure(w http.ResponseWriter, failure *validateFailure) {
	payload := map[string]any{"error": failure.message, "code": failure.code}
	if failure.field != "" {
		payload["field"] = failure.field
	}
	writeJSON(w, failure.status, payload)
}
