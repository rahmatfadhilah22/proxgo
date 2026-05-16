package main

import (
	"net/http"
	"testing"
	"time"

	"proxgo/internal/config"
)

func TestNewHTTPServerAppliesTimeouts(t *testing.T) {
	cfg := config.Config{Port: "8080"}
	handler := http.NewServeMux()

	srv := newHTTPServer(cfg, handler)

	if srv.Addr != ":8080" {
		t.Fatalf("expected addr :8080, got %q", srv.Addr)
	}
	if srv.Handler != handler {
		t.Fatal("expected handler to be assigned")
	}
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("expected ReadHeaderTimeout 5s, got %s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 30*time.Second {
		t.Fatalf("expected ReadTimeout 30s, got %s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 60*time.Second {
		t.Fatalf("expected WriteTimeout 60s, got %s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Fatalf("expected IdleTimeout 120s, got %s", srv.IdleTimeout)
	}
}
