package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeTokenValueRaw(t *testing.T) {
	raw := strings.Repeat("y0_AgAA", 8)
	out, err := normalizeTokenValue(" " + raw + " ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if parsed["access_token"] != raw {
		t.Fatalf("access_token = %v, want raw token", parsed["access_token"])
	}
}

func TestNormalizeTokenValueJSON(t *testing.T) {
	in := `{"access_token":"abc","refresh_token":"r","expiry":"2030-01-01T00:00:00Z"}`
	out, err := normalizeTokenValue(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != in {
		t.Fatalf("JSON token was rewritten: %s", out)
	}
}

func TestNormalizeTokenValueRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "   ", "short", "{\"foo\":\"bar\"}", "token with spaces inside value ok but too short"} {
		if _, err := normalizeTokenValue(in); err == nil {
			t.Fatalf("expected error for %q", in)
		}
	}
}
