package main

import (
	"context"
	"log"
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
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
