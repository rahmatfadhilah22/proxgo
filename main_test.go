package main

import (
	"net/http"
	"net/http/httptest"
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
	if srv.Handler == nil {
		t.Fatal("expected handler to be assigned")
	}
	if srv.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("expected ReadHeaderTimeout 5s, got %s", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 30*time.Second {
		t.Fatalf("expected ReadTimeout 30s, got %s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("expected WriteTimeout 0 for streaming responses, got %s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Fatalf("expected IdleTimeout 120s, got %s", srv.IdleTimeout)
	}
}

func TestWithIdleWriteTimeoutInvokesWrappedHandler(t *testing.T) {
	called := false
	wrapped := withIdleWriteTimeout(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}), downstreamWriteIdleTimeout)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected wrapped handler to be called")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestDeadlineResponseWriterUnwrapsOriginalWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	wrapped := &deadlineResponseWriter{
		ResponseWriter: rec,
		controller:     http.NewResponseController(rec),
		timeout:        downstreamWriteIdleTimeout,
	}

	if wrapped.Unwrap() != rec {
		t.Fatal("expected Unwrap to return the original response writer")
	}
}
