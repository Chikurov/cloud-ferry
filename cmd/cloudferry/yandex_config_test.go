package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestEnvEitherPrefersNewName(t *testing.T) {
	t.Setenv("CF_YANDEX_CLIENT_ID", "cf-id")
	t.Setenv("YANDEX_CLIENT_ID", "alias-id")

	if got := envEither("CF_YANDEX_CLIENT_ID", "YANDEX_CLIENT_ID"); got != "cf-id" {
		t.Fatalf("envEither = %q, want the CF_ name first", got)
	}
}

func TestEnvEitherFallsBackToAlias(t *testing.T) {
	t.Setenv("CF_YANDEX_CLIENT_ID", "")
	t.Setenv("YANDEX_CLIENT_ID", "alias-id")

	if got := envEither("CF_YANDEX_CLIENT_ID", "YANDEX_CLIENT_ID"); got != "alias-id" {
		t.Fatalf("envEither = %q, want the alias", got)
	}
}

// The startup log may name the public client id, but the secret must never
// appear even when only half of the pair is configured.
func TestYandexAppCredentialsLogOnlyClientID(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	const secret = "super-secret-value"
	logYandexAppCredentials("client-123", secret)
	logYandexAppCredentials("", secret)
	logYandexAppCredentials("client-123", "")

	logs := buf.String()
	if !strings.Contains(logs, "client id client-123") {
		t.Fatalf("log does not name the client id: %q", logs)
	}
	if strings.Contains(logs, secret) {
		t.Fatalf("client secret leaked into the log: %q", logs)
	}
}
