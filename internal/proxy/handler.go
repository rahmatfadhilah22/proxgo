package proxy

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"proxgo/internal/httpjson"
	"proxgo/internal/pool"
)

type tokenPool interface {
	PickExcluding(excluded map[string]struct{}) (string, error)
	MarkCooldown(token string, duration time.Duration)
	Size() int
}

type Handler struct {
	client           *http.Client
	baseURL          *url.URL
	pool             tokenPool
	cooldownDuration time.Duration
}

func NewHandler(baseURL *url.URL, tokenPool tokenPool, cooldownDuration time.Duration) *Handler {
	return &Handler{
		client:           &http.Client{},
		baseURL:          cloneURL(baseURL),
		pool:             tokenPool,
		cooldownDuration: cooldownDuration,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read request body")
		return
	}

	_ = r.Body.Close()

	usedTokens := make(map[string]struct{})
	serverRetryUsed := false

	for len(usedTokens) < h.pool.Size() {
		token, err := h.pool.PickExcluding(usedTokens)
		if err != nil {
			httpjson.WriteError(w, http.StatusServiceUnavailable, "all tokens are rate limited, please retry later")
			log.Printf("[ERROR] %s %s -> all tokens unavailable", r.Method, r.URL.Path)
			return
		}

		usedTokens[token] = struct{}{}

		resp, duration, err := h.forward(r, body, token)
		if err != nil {
			httpjson.WriteError(w, http.StatusBadGateway, "upstream request failed")
			log.Printf("[ERROR] %s %s -> token=%s -> upstream error: %v", r.Method, r.URL.Path, tokenLabel(token), err)
			return
		}

		if isServerError(resp.StatusCode) && !serverRetryUsed {
			retryToken, retryErr := h.pool.PickExcluding(usedTokens)
			if retryErr == nil {
				serverRetryUsed = true
				usedTokens[retryToken] = struct{}{}
				closeBody(resp.Body)

				resp, duration, err = h.forward(r, body, retryToken)
				token = retryToken
				if err != nil {
					httpjson.WriteError(w, http.StatusBadGateway, "upstream request failed")
					log.Printf("[ERROR] %s %s -> token=%s -> upstream error after retry: %v", r.Method, r.URL.Path, tokenLabel(token), err)
					return
				}
			}
		}

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			h.pool.MarkCooldown(token, h.cooldownDuration)
			log.Printf("[WARN] %s %s -> token=%s -> 429, marking cooldown %s", r.Method, r.URL.Path, tokenLabel(token), h.cooldownDuration)
			closeBody(resp.Body)
			continue
		case isServerError(resp.StatusCode):
			log.Printf("[WARN] %s %s -> token=%s -> upstream %d after %s", r.Method, r.URL.Path, tokenLabel(token), resp.StatusCode, duration)
			writeUpstreamResponse(w, resp)
			return
		default:
			log.Printf("[INFO] %s %s -> token=%s -> %d (%s)", r.Method, r.URL.Path, tokenLabel(token), resp.StatusCode, duration)
			writeUpstreamResponse(w, resp)
			return
		}
	}

	httpjson.WriteError(w, http.StatusServiceUnavailable, "all tokens are rate limited, please retry later")
	log.Printf("[ERROR] %s %s -> all tokens rate limited", r.Method, r.URL.Path)
}

func (h *Handler) forward(r *http.Request, body []byte, token string) (*http.Response, time.Duration, error) {
	upstreamURL := h.buildUpstreamURL(r.URL)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}

	copyHeaders(req.Header, r.Header)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Host = h.baseURL.Host

	start := time.Now()
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, 0, err
	}

	return resp, time.Since(start), nil
}

func (h *Handler) buildUpstreamURL(requestURL *url.URL) *url.URL {
	target := cloneURL(h.baseURL)
	target.Path = joinPath(h.baseURL.Path, requestURL.Path)
	target.RawPath = target.Path
	target.RawQuery = requestURL.RawQuery
	return target
}

func joinPath(basePath, requestPath string) string {
	basePath = strings.TrimRight(basePath, "/")
	if requestPath == "" {
		requestPath = "/"
	}

	if basePath == "" {
		return requestPath
	}

	if requestPath == basePath || strings.HasPrefix(requestPath, basePath+"/") {
		return requestPath
	}

	return basePath + "/" + strings.TrimLeft(requestPath, "/")
}

func cloneURL(value *url.URL) *url.URL {
	cloned := *value
	return &cloned
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, "Authorization") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func writeUpstreamResponse(w http.ResponseWriter, resp *http.Response) {
	defer closeBody(resp.Body)

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func closeBody(closer io.Closer) {
	_ = closer.Close()
}

func isServerError(status int) bool {
	return status >= 500 && status <= 599
}

func tokenLabel(token string) string {
	if len(token) <= 6 {
		return token
	}
	return fmt.Sprintf("%s...", token[:6])
}

var _ tokenPool = (*pool.TokenPool)(nil)
