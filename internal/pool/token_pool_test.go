package pool

import (
	"testing"
	"time"
)

func TestPickRotatesTokensRoundRobin(t *testing.T) {
	pool := NewTokenPool([]string{"token-a", "token-b"})

	first, err := pool.Pick()
	if err != nil {
		t.Fatalf("expected first pick to succeed: %v", err)
	}

	second, err := pool.Pick()
	if err != nil {
		t.Fatalf("expected second pick to succeed: %v", err)
	}

	if first != "token-a" || second != "token-b" {
		t.Fatalf("expected round-robin picks token-a then token-b, got %q then %q", first, second)
	}
}

func TestPickSkipsTokensInCooldown(t *testing.T) {
	pool := NewTokenPool([]string{"token-a", "token-b"})
	pool.MarkCooldown("token-a", time.Minute)

	pick, err := pool.Pick()
	if err != nil {
		t.Fatalf("expected pick to succeed: %v", err)
	}

	if pick != "token-b" {
		t.Fatalf("expected token-b, got %q", pick)
	}
}

func TestPickReturnsErrorWhenAllTokensCoolingDown(t *testing.T) {
	pool := NewTokenPool([]string{"token-a", "token-b"})
	pool.MarkCooldown("token-a", time.Minute)
	pool.MarkCooldown("token-b", time.Minute)

	_, err := pool.Pick()
	if err == nil {
		t.Fatal("expected pick to fail when all tokens are cooling down")
	}
}
