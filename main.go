package main

import (
	"log"
	"net/http"
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

	server := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: server.NewHandler(cfg.GatewayAPIKey, proxyHandler),
	}

	log.Printf("[INFO] Token Gateway started on :%s (%d tokens loaded)", cfg.Port, tokenPool.Size())

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("[ERROR] server failed: %v", err)
	}
}
