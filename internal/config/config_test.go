package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsNonPositiveCooldown(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "secret")
	t.Setenv("UPSTREAM_TOKENS", "token-a,token-b")
	t.Setenv("UPSTREAM_BASE_URL", "https://example.com/v1")
	t.Setenv("TOKEN_COOLDOWN_SECONDS", "0")
	t.Setenv("PORT", "8080")

	_, err := Load()
	if err == nil {
		t.Fatal("expected config load to fail for non-positive cooldown")
	}
	if !strings.Contains(err.Error(), "TOKEN_COOLDOWN_SECONDS") {
		t.Fatalf("expected cooldown validation error, got %v", err)
	}
}
