package httpapi

import (
	"encoding/json"
	"errors"
	"strings"
)

// normalizeTokenValue accepts either a full rclone token JSON or a raw OAuth
// access token. A raw token is wrapped into the JSON shape rclone expects; it
// carries no refresh token, so the account simply cannot renew access once the
// token expires, which is acceptable for long-lived Yandex tokens.
func normalizeTokenValue(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return "", errors.New("Вставьте токен доступа.")
	}
	if strings.HasPrefix(token, "{") {
		var parsed struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.Unmarshal([]byte(token), &parsed); err != nil || parsed.AccessToken == "" {
			return "", errors.New("В токене нет поля access_token. Вставьте токен или JSON из rclone.")
		}
		return token, nil
	}
	if len(token) < 20 || strings.ContainsAny(token, " \n\t\"") {
		return "", errors.New("Токен выглядит неполным. Скопируйте его целиком.")
	}
	wrapped, err := json.Marshal(map[string]any{"access_token": token, "token_type": "Bearer"})
	if err != nil {
		return "", err
	}
	return string(wrapped), nil
}
