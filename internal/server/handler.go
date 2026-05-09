package server

import (
	"encoding/json"
	"net/http"

	"proxgo/internal/auth"
)

func NewHandler(gatewayAPIKey string, proxyHandler http.Handler) http.Handler {
	protected := auth.Middleware(gatewayAPIKey, proxyHandler)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
			return
		}

		protected.ServeHTTP(w, r)
	})
}
