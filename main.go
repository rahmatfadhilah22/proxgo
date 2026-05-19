package main

import (
	"bufio"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"proxgo/internal/config"
	"proxgo/internal/pool"
	"proxgo/internal/proxy"
	"proxgo/internal/server"
)

const downstreamWriteIdleTimeout = 30 * time.Second

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[ERROR] %v", err)
	}

	tokenPool := pool.NewTokenPool(cfg.UpstreamTokens)
	proxyHandler := proxy.NewHandler(cfg.UpstreamBaseURL, tokenPool, time.Duration(cfg.TokenCooldownSeconds)*time.Second)

	srv := newHTTPServer(cfg, server.NewHandler(cfg.GatewayAPIKey, proxyHandler))

	log.Printf("[INFO] Token Gateway started on :%s (%d tokens loaded)", cfg.Port, tokenPool.Size())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			log.Fatalf("[ERROR] server failed: %v", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("[ERROR] server shutdown failed: %v", err)
		}
	}
}

func newHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           withIdleWriteTimeout(handler, downstreamWriteIdleTimeout),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}
}

func withIdleWriteTimeout(next http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&deadlineResponseWriter{
			ResponseWriter: w,
			controller:     http.NewResponseController(w),
			timeout:        timeout,
		}, r)
	})
}

type deadlineResponseWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
}

func (w *deadlineResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *deadlineResponseWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *deadlineResponseWriter) Write(p []byte) (int, error) {
	w.setWriteDeadline()
	return w.ResponseWriter.Write(p)
}

func (w *deadlineResponseWriter) Flush() {
	w.setWriteDeadline()
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *deadlineResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *deadlineResponseWriter) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (w *deadlineResponseWriter) setWriteDeadline() {
	if err := w.controller.SetWriteDeadline(time.Now().Add(w.timeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Printf("[WARN] failed to set downstream write deadline: %v", err)
	}
}
