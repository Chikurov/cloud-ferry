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

// DeviceAuth is the pair of codes returned by the device authorization grant.
type DeviceAuth struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

// RequestDeviceCode starts the device flow. The user enters UserCode on
// VerificationURL from any device with a browser.
func RequestDeviceCode(ctx context.Context, clientID, deviceID, deviceName string) (DeviceAuth, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return DeviceAuth{}, errors.New("нужен client_id")
	}

	form := url.Values{}
	form.Set("client_id", clientID)
	if deviceID != "" {
		form.Set("device_id", deviceID)
	}
	if deviceName != "" {
		form.Set("device_name", deviceName)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint+"/device/code", strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceAuth{}, fmt.Errorf("request device code: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return DeviceAuth{}, fmt.Errorf("request device code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return DeviceAuth{}, fmt.Errorf("read device code response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
			return DeviceAuth{}, &OAuthError{Code: apiErr.Error, Description: apiErr.Description}
		}
		return DeviceAuth{}, fmt.Errorf("яндекс: HTTP %d", resp.StatusCode)
	}

	var auth DeviceAuth
	if err := json.Unmarshal(body, &auth); err != nil {
		return DeviceAuth{}, fmt.Errorf("decode device code: %w", err)
	}
	if auth.DeviceCode == "" || auth.UserCode == "" {
		return DeviceAuth{}, errors.New("яндекс не вернул коды устройства")
	}
	if auth.VerificationURL == "" {
		auth.VerificationURL = "https://oauth.yandex.ru/device"
	}
	if auth.Interval <= 0 {
		auth.Interval = 5
	}
	if auth.ExpiresIn <= 0 {
		auth.ExpiresIn = 300
	}
	return auth, nil
}

// PollDeviceToken asks for the token once. pending is true while the user has
// not confirmed the code yet; the caller should retry after Interval.
func PollDeviceToken(ctx context.Context, clientID, clientSecret, deviceCode string) (token Token, pending bool, err error) {
	form := url.Values{}
	form.Set("grant_type", "device_code")
	form.Set("code", deviceCode)
	form.Set("client_id", strings.TrimSpace(clientID))
	if secret := strings.TrimSpace(clientSecret); secret != "" {
		form.Set("client_secret", secret)
	}

	token, err = postToken(ctx, form)
	var oauthErr *OAuthError
	if errors.As(err, &oauthErr) && oauthErr.Code == "authorization_pending" {
		return Token{}, true, nil
	}
	if err != nil {
		return Token{}, false, err
	}
	return token, false, nil
}

// DeviceCodeExpiry is a convenience for callers that show a countdown.
func DeviceCodeExpiry(auth DeviceAuth, now time.Time) time.Time {
	return now.Add(time.Duration(auth.ExpiresIn) * time.Second)
}
