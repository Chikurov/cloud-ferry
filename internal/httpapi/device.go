package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"cloudferry/internal/store"
	"cloudferry/internal/yandex"
)

// deviceFlow is a pending device authorization: the user is entering userCode
// on Yandex while we poll for the token.
type deviceFlow struct {
	pollMu       sync.Mutex
	id           string
	clientID     string
	clientSecret string
	name         string
	deviceCode   string
	expiresAt    time.Time
}

const deviceDeviceID = "umbrel-cloud-ferry"
const deviceDeviceName = "Umbrel"

// Each pending flow holds a Yandex device code and one of our slots until it is
// confirmed or expires, so an unbounded number of starts would let one caller
// exhaust the server. The lifetime cap also keeps an abandoned flow from
// occupying a slot for long.
const (
	maxDeviceFlows        = 5
	deviceFlowMaxLifetime = 10 * time.Minute
)

type deviceStartRequest struct {
	Name         string `json:"name"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

func (s *Server) handleDeviceStart(w http.ResponseWriter, r *http.Request) {
	var req deviceStartRequest
	if err := decodeJSON(r, &req); err != nil {
		s.badRequest(w, err)
		return
	}

	// The server's own OAuth app takes priority: with it configured the user
	// only enters the confirmation code. Credentials in the body are still
	// accepted when the server has none, so older clients keep working.
	clientID, clientSecret, serverCredentials := s.yandexServerCredentials()
	if !serverCredentials {
		clientID = strings.TrimSpace(req.ClientID)
		clientSecret = strings.TrimSpace(req.ClientSecret)
	}
	if clientID == "" || clientSecret == "" {
		writeError(w, http.StatusBadRequest, "Нужны ID и секрет приложения: без них Яндекс не выдаст код.")
		return
	}
	if !s.reserveDeviceFlow(time.Now()) {
		writeError(w, http.StatusTooManyRequests, "Слишком много одновременных входов. Завершите начатый или повторите через несколько минут.")
		return
	}
	// Only the public client id may be logged: the secret stays out of logs.
	log.Printf("yandex oauth: device flow started for client id %s", clientID)

	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	auth, err := yandex.RequestDeviceCode(ctx, clientID, deviceDeviceID, deviceDeviceName)
	if err != nil {
		s.releaseDeviceFlowReservation()
		s.fail(w, http.StatusBadGateway, oauthMessage(err), err)
		return
	}

	lifetime := deviceFlowLifetime(auth)
	flow := &deviceFlow{
		id:           randomID(),
		clientID:     clientID,
		clientSecret: clientSecret,
		name:         limitName(req.Name),
		deviceCode:   auth.DeviceCode,
		expiresAt:    time.Now().Add(lifetime),
	}
	s.storeDeviceFlow(flow)

	writeJSON(w, http.StatusOK, map[string]any{
		"flowId":          flow.id,
		"userCode":        auth.UserCode,
		"verificationUrl": auth.VerificationURL,
		"interval":        auth.Interval,
		"expiresIn":       int(lifetime / time.Second),
	})
}

// handleYandexStatus tells the UI whether the app has its own OAuth app, so it
// either hides the client credentials or asks for them. The credentials
// themselves are never returned.
func (s *Server) handleYandexStatus(w http.ResponseWriter, r *http.Request) {
	_, _, available := s.yandexServerCredentials()
	writeJSON(w, http.StatusOK, map[string]bool{"available": available})
}

// yandexServerCredentials reports the app's own OAuth app credentials. Both
// parts must be set: a lone client id cannot refresh a token later.
func (s *Server) yandexServerCredentials() (clientID, clientSecret string, ok bool) {
	return s.cfg.YandexClientID, s.cfg.YandexClientSecret,
		s.cfg.YandexClientID != "" && s.cfg.YandexClientSecret != ""
}

func (s *Server) handleDeviceStatus(w http.ResponseWriter, r *http.Request) {
	flow, ok := s.takeFlow(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Код устарел. Начните вход заново.")
		return
	}
	flow.pollMu.Lock()
	defer flow.pollMu.Unlock()
	if _, live := s.takeFlow(flow.id); !live {
		writeError(w, http.StatusNotFound, "Этот вход уже завершён.")
		return
	}

	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	token, pending, err := yandex.PollDeviceToken(ctx, flow.clientID, flow.clientSecret, flow.deviceCode)
	if pending {
		writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
		return
	}
	if err != nil {
		s.dropFlow(flow.id)
		writeJSON(w, http.StatusOK, map[string]string{"status": "error", "message": oauthMessage(err)})
		return
	}

	// Claim completion before writing credentials. A canceled or expired
	// flow cannot be revived by a late provider response.
	if !s.consumeFlow(flow) {
		writeError(w, http.StatusNotFound, "Вход отменён или код истёк.")
		return
	}
	account, err := s.createYandexAccount(ctx, flow.name, flow.clientID, flow.clientSecret, token)
	if err != nil {
		s.fail(w, http.StatusBadGateway, "Не удалось сохранить подключение. Попробуйте ещё раз.", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "done", "account": account})
}

func (s *Server) handleDeviceCancel(w http.ResponseWriter, r *http.Request) {
	s.dropFlow(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"status": "canceled"})
}

// takeFlow returns a live flow and removes expired ones.
func (s *Server) takeFlow(id string) (*deviceFlow, bool) {
	s.flowsMu.Lock()
	defer s.flowsMu.Unlock()

	flow, ok := s.flows[id]
	if !ok {
		return nil, false
	}
	if time.Now().After(flow.expiresAt) {
		delete(s.flows, id)
		return nil, false
	}
	return flow, true
}

func (s *Server) consumeFlow(flow *deviceFlow) bool {
	s.flowsMu.Lock()
	defer s.flowsMu.Unlock()
	if s.flows[flow.id] != flow {
		return false
	}
	delete(s.flows, flow.id)
	return time.Now().Before(flow.expiresAt)
}

func (s *Server) dropFlow(id string) {
	s.flowsMu.Lock()
	defer s.flowsMu.Unlock()
	delete(s.flows, id)
}

// reserveDeviceFlow claims one of the limited concurrent-flow slots. Expired
// flows are pruned first, so an abandoned flow never blocks a new start. The
// reservation is held while Yandex issues the device code, so simultaneous
// starts cannot overshoot the limit.
func (s *Server) reserveDeviceFlow(now time.Time) bool {
	s.flowsMu.Lock()
	defer s.flowsMu.Unlock()
	s.pruneFlowsLocked(now)
	if len(s.flows)+s.flowReservations >= maxDeviceFlows {
		return false
	}
	s.flowReservations++
	return true
}

// storeDeviceFlow records a started flow and consumes its reservation.
func (s *Server) storeDeviceFlow(flow *deviceFlow) {
	s.flowsMu.Lock()
	defer s.flowsMu.Unlock()
	if s.flowReservations > 0 {
		s.flowReservations--
	}
	s.flows[flow.id] = flow
}

// releaseDeviceFlowReservation gives the slot back when Yandex rejected the
// start, so a failed request does not count against the limit.
func (s *Server) releaseDeviceFlowReservation() {
	s.flowsMu.Lock()
	defer s.flowsMu.Unlock()
	if s.flowReservations > 0 {
		s.flowReservations--
	}
}

func (s *Server) pruneFlowsLocked(now time.Time) {
	for id, flow := range s.flows {
		if now.After(flow.expiresAt) {
			delete(s.flows, id)
		}
	}
}

// deviceFlowLifetime keeps a pending flow for at most ten minutes, even when
// Yandex offers a longer window: a slot must not be held indefinitely.
func deviceFlowLifetime(auth yandex.DeviceAuth) time.Duration {
	lifetime := time.Duration(auth.ExpiresIn) * time.Second
	if lifetime <= 0 || lifetime > deviceFlowMaxLifetime {
		return deviceFlowMaxLifetime
	}
	return lifetime
}

// createYandexAccount writes the token into rclone and records the account.
func (s *Server) createYandexAccount(ctx context.Context, name, clientID, clientSecret string, token yandex.Token) (store.Account, error) {
	tokenJSON, err := token.RcloneToken(time.Now())
	if err != nil {
		return store.Account{}, err
	}
	id, err := newID()
	if err != nil {
		return store.Account{}, err
	}
	remote := "cf_" + id

	parameters := map[string]any{"client_id": clientID, "token": tokenJSON}
	if secret := strings.TrimSpace(clientSecret); secret != "" {
		parameters["client_secret"] = secret
	}
	call := map[string]any{
		"name":       remote,
		"type":       "yandex",
		"parameters": parameters,
		"opt":        map[string]any{"nonInteractive": true},
	}
	if err := s.cfg.Rclone.Call(ctx, "config/create", call, nil); err != nil {
		return store.Account{}, err
	}

	if name = limitName(name); name == "" {
		name = "Яндекс Диск"
	}
	account := store.Account{
		ID:        id,
		Name:      name,
		Provider:  "yandex-oauth",
		Remote:    remote,
		CreatedAt: time.Now().UTC(),
		Status:    store.AccountStatusUnknown,
	}
	if err := s.cfg.Store.AddAccount(account); err != nil {
		s.rollbackRemote(remote)
		return store.Account{}, err
	}
	return account, nil
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().UTC().Format("20060102150405")
	}
	return hex.EncodeToString(buf)
}
