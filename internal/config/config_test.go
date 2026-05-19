package config

import (
	"strings"
	"testing"
)

func TestLoadUsesDefaultMaxRequestBodyMB(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "secret")
	t.Setenv("UPSTREAM_TOKENS", "token-a,token-b")
	t.Setenv("UPSTREAM_BASE_URL", "https://example.com/v1")
	t.Setenv("TOKEN_COOLDOWN_SECONDS", "60")
	t.Setenv("PORT", "8080")
	t.Setenv("MAX_REQUEST_BODY_MB", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config load to succeed, got %v", err)
	}
	if cfg.MaxRequestBodyBytes != 10*1024*1024 {
		t.Fatalf("expected default max request body 10MiB, got %d", cfg.MaxRequestBodyBytes)
	}
}

func TestLoadUsesConfiguredMaxRequestBodyMB(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "secret")
	t.Setenv("UPSTREAM_TOKENS", "token-a,token-b")
	t.Setenv("UPSTREAM_BASE_URL", "https://example.com/v1")
	t.Setenv("TOKEN_COOLDOWN_SECONDS", "60")
	t.Setenv("PORT", "8080")
	t.Setenv("MAX_REQUEST_BODY_MB", "12")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config load to succeed, got %v", err)
	}
	if cfg.MaxRequestBodyBytes != 12*1024*1024 {
		t.Fatalf("expected configured max request body 12MiB, got %d", cfg.MaxRequestBodyBytes)
	}
}

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

func TestLoadRejectsNonPositiveMaxRequestBodyMB(t *testing.T) {
	t.Setenv("GATEWAY_API_KEY", "secret")
	t.Setenv("UPSTREAM_TOKENS", "token-a,token-b")
	t.Setenv("UPSTREAM_BASE_URL", "https://example.com/v1")
	t.Setenv("TOKEN_COOLDOWN_SECONDS", "60")
	t.Setenv("MAX_REQUEST_BODY_MB", "0")
	t.Setenv("PORT", "8080")

	_, err := Load()
	if err == nil {
		t.Fatal("expected config load to fail for non-positive max request body")
	}
	if !strings.Contains(err.Error(), "MAX_REQUEST_BODY_MB") {
		t.Fatalf("expected max request body validation error, got %v", err)
	}
}
