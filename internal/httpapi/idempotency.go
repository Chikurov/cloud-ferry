package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
)

// maxIdempotencyKeyLen is the longest accepted key. Together with
// minIdempotencyKeyLen it matches the contract: a short key is too easy to
// collide by accident, and a huge one would only bloat the state file.
const (
	minIdempotencyKeyLen = 8
	maxIdempotencyKeyLen = 128
)

// idempotencyKeyPattern is the accepted shape of a key: Latin letters, digits
// and the separators . _ : -. The narrow alphabet keeps the key printable,
// comparable and safe in both a header and a JSON field.
var idempotencyKeyPattern = regexp.MustCompile(fmt.Sprintf(
	`^[A-Za-z0-9._:-]{%d,%d}$`, minIdempotencyKeyLen, maxIdempotencyKeyLen))

// errInvalidIdempotencyKey is user-facing: it says what a key must look like.
var errInvalidIdempotencyKey = errors.New("Ключ идемпотентности: от 8 до 128 символов — латинские буквы, цифры и символы . _ : -.")

// requestIdempotencyKey resolves the optional key of a create-job request. The
// Idempotency-Key header has priority; without it the clientToken field is
// used. field names where the key came from, so an error answer can point at
// the right place; an absent key is valid and simply means no idempotency.
func requestIdempotencyKey(r *http.Request, token string) (key, field string, err error) {
	key, field = r.Header.Get("Idempotency-Key"), "Idempotency-Key"
	if key == "" {
		key, field = token, "clientToken"
	}
	if key == "" {
		return "", "", nil
	}
	if !idempotencyKeyPattern.MatchString(key) {
		return "", field, errInvalidIdempotencyKey
	}
	return key, field, nil
}

// writeIdempotencyError answers an idempotency problem. The body keeps the usual
// error sentence and field and adds the stable code from the contract, so a
// client can react without parsing the message.
func writeIdempotencyError(w http.ResponseWriter, status int, code, field, message string) {
	body := map[string]string{"error": message, "code": code}
	if field != "" {
		body["field"] = field
	}
	writeJSON(w, status, body)
}
