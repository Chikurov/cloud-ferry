package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"cloudferry/internal/store"
	"cloudferry/internal/yandex"
)

// deviceFlow is a pending device authorization: the user is entering userCode
// on Yandex while we poll for the token.
type deviceFlow struct {
	id           string
	clientID     string
	clientSecret string
	name         string
	deviceCode   string
	expiresAt    time.Time
}

const deviceDeviceID = "umbrel-cloud-ferry"
const deviceDeviceName = "Umbrel"

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
	clientID := strings.TrimSpace(req.ClientID)
	clientSecret := strings.TrimSpace(req.ClientSecret)
	if clientID == "" || clientSecret == "" {
		writeError(w, http.StatusBadRequest, "Нужны ID и секрет приложения: без них Яндекс не выдаст код.")
		return
	}

	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	auth, err := yandex.RequestDeviceCode(ctx, clientID, deviceDeviceID, deviceDeviceName)
	if err != nil {
		s.fail(w, http.StatusBadGateway, oauthMessage(err), err)
		return
	}

	flow := &deviceFlow{
		id:           randomID(),
		clientID:     clientID,
		clientSecret: clientSecret,
		name:         limitName(req.Name),
		deviceCode:   auth.DeviceCode,
		expiresAt:    time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second),
	}
	s.flowsMu.Lock()
	s.flows[flow.id] = flow
	s.flowsMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"flowId":          flow.id,
		"userCode":        auth.UserCode,
		"verificationUrl": auth.VerificationURL,
		"interval":        auth.Interval,
		"expiresIn":       auth.ExpiresIn,
	})
}

func (s *Server) handleDeviceStatus(w http.ResponseWriter, r *http.Request) {
	flow, ok := s.takeFlow(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Код устарел. Начните вход заново.")
		return
	}
	s.flowsMu.Lock()
	s.flows[flow.id] = flow
	s.flowsMu.Unlock()

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

	account, err := s.createYandexAccount(ctx, flow.name, flow.clientID, flow.clientSecret, token)
	s.dropFlow(flow.id)
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

func (s *Server) dropFlow(id string) {
	s.flowsMu.Lock()
	defer s.flowsMu.Unlock()
	delete(s.flows, id)
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
