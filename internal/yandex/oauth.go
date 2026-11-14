// Package yandex implements the OAuth "verification code" flow for Yandex ID:
// the user opens a link on any device, copies the 7-digit code and pastes it
// into the app, which exchanges it for an access/refresh token pair. This works
// on a headless server with no public URL and no TLS certificate.
package yandex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultEndpoint is Yandex's OAuth host.
const DefaultEndpoint = "https://oauth.yandex.ru"

// Endpoint is a variable so tests can point at a local server.
var Endpoint = DefaultEndpoint

// HTTPClient is used for token exchange.
var HTTPClient = &http.Client{Timeout: 30 * time.Second}

// VerificationRedirect must be registered as the app's Redirect URI; Yandex
// then shows the confirmation code in the browser instead of redirecting.
const VerificationRedirect = "https://oauth.yandex.ru/verification_code"

// AuthURL builds the page the user must open.
func AuthURL(clientID string) string {
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", strings.TrimSpace(clientID))
	query.Set("redirect_uri", VerificationRedirect)
	return Endpoint + "/authorize?" + query.Encode()
}

// OAuthError is a rejected token request with Yandex's error code, so callers
// can map it to a human explanation.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return "яндекс: " + e.Description
	}
	return "яндекс: " + e.Code
}

// Exchange trades the confirmation code for tokens.
func Exchange(ctx context.Context, clientID, clientSecret, code string) (Token, error) {
	clientID = strings.TrimSpace(clientID)
	code = strings.TrimSpace(code)
	if clientID == "" || code == "" {
		return Token{}, errors.New("нужны client_id и код подтверждения")
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", clientID)
	if secret := strings.TrimSpace(clientSecret); secret != "" {
		form.Set("client_secret", secret)
	}
	return postToken(ctx, form)
}

// postToken sends a form to the token endpoint and decodes the response.
func postToken(ctx context.Context, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("request token: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("exchange code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
			return Token{}, &OAuthError{Code: apiErr.Error, Description: apiErr.Description}
		}
		return Token{}, fmt.Errorf("яндекс: HTTP %d", resp.StatusCode)
	}

	var token Token
	if err := json.Unmarshal(body, &token); err != nil {
		return Token{}, fmt.Errorf("decode token: %w", err)
	}
	if token.AccessToken == "" {
		return Token{}, errors.New("яндекс не вернул access_token")
	}
	return token, nil
}

// Token is the result of a successful exchange.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// RcloneToken renders the token in the JSON shape rclone stores in its config.
// rclone refreshes it with the app's client_id and client_secret, so those must
// be written to the config alongside.
func (t Token) RcloneToken(now time.Time) (string, error) {
	expiry := now.Add(time.Duration(t.ExpiresIn) * time.Second)
	if t.ExpiresIn <= 0 {
		expiry = now.AddDate(1, 0, 0)
	}
	payload := map[string]string{
		"access_token":  t.AccessToken,
		"token_type":    "OAuth",
		"refresh_token": t.RefreshToken,
		"expiry":        expiry.UTC().Format(time.RFC3339),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode token: %w", err)
	}
	return string(encoded), nil
}
