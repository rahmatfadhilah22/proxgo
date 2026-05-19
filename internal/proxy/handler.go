package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"proxgo/internal/httpjson"
	"proxgo/internal/pool"
)

const upstreamBodyIdleTimeout = 2 * time.Minute

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
	maxRequestBodyBytes int64
}

func NewHandler(baseURL *url.URL, tokenPool tokenPool, cooldownDuration time.Duration, maxRequestBodyBytes int64) *Handler {
	return &Handler{
		client:              newHTTPClient(),
		baseURL:             cloneURL(baseURL),
		pool:                tokenPool,
		cooldownDuration:    cooldownDuration,
		maxRequestBodyBytes: maxRequestBodyBytes,
	}
}

func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 60 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second
	transport.IdleConnTimeout = 90 * time.Second

	return &http.Client{Transport: transport}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, h.maxRequestBodyBytes+1))
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "failed to read request body")
		return
	}
	if int64(len(body)) > h.maxRequestBodyBytes {
		httpjson.WriteError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	_ = r.Body.Close()

	usedTokens := make(map[string]struct{})
	serverRetryUsed := false
	transportRetryUsed := false

	for len(usedTokens) < h.pool.Size() {
		token, err := h.pool.PickExcluding(usedTokens)
		if err != nil {
			httpjson.WriteError(w, http.StatusServiceUnavailable, "all tokens are rate limited, please retry later")
			log.Printf("[ERROR] %s %s -> all tokens unavailable", r.Method, r.URL.Path)
			return
		}

		usedTokens[token] = struct{}{}

		resp, duration, cancel, err := h.forward(r, body, token)
		if err != nil {
			if !transportRetryUsed && shouldRetryTransportError(err) {
				retryToken, retryErr := h.pool.PickExcluding(usedTokens)
				if retryErr == nil {
					transportRetryUsed = true
					usedTokens[retryToken] = struct{}{}

					resp, duration, cancel, err = h.forward(r, body, retryToken)
					token = retryToken
					if err == nil {
						goto handleResponse
					}
				}
			}

			httpjson.WriteError(w, http.StatusBadGateway, "upstream request failed")
			log.Printf("[ERROR] %s %s -> token=%s -> upstream error: %v", r.Method, r.URL.Path, tokenLabel(token), err)
			return
		}

	handleResponse:
		if isServerError(resp.StatusCode) && !serverRetryUsed {
			retryToken, retryErr := h.pool.PickExcluding(usedTokens)
			if retryErr == nil {
				serverRetryUsed = true
				usedTokens[retryToken] = struct{}{}
				closeBody(resp.Body)

				cancel()
				resp, duration, cancel, err = h.forward(r, body, retryToken)
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
			cancel()
			h.pool.MarkCooldown(token, h.cooldownDuration)
			log.Printf("[WARN] %s %s -> token=%s -> 429, marking cooldown %s", r.Method, r.URL.Path, tokenLabel(token), h.cooldownDuration)
			closeBody(resp.Body)
			continue
		case isServerError(resp.StatusCode):
			log.Printf("[WARN] %s %s -> token=%s -> upstream %d after %s", r.Method, r.URL.Path, tokenLabel(token), resp.StatusCode, duration)
			writeUpstreamResponse(w, resp, cancel)
			return
		default:
			log.Printf("[INFO] %s %s -> token=%s -> %d (%s)", r.Method, r.URL.Path, tokenLabel(token), resp.StatusCode, duration)
			writeUpstreamResponse(w, resp, cancel)
			return
		}
	}

	httpjson.WriteError(w, http.StatusServiceUnavailable, "all tokens are rate limited, please retry later")
	log.Printf("[ERROR] %s %s -> all tokens rate limited", r.Method, r.URL.Path)
}


func (h *Handler) forward(r *http.Request, body []byte, token string) (*http.Response, time.Duration, context.CancelFunc, error) {
	upstreamURL := h.buildUpstreamURL(r.URL)
	ctx, cancel := context.WithCancel(r.Context())
	req, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL.String(), bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, 0, nil, err
	}

	copyHeaders(req.Header, r.Header)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Host = h.baseURL.Host

	start := time.Now()
	resp, err := h.client.Do(req)
	if err != nil {
		cancel()
		return nil, 0, nil, err
	}

	return resp, time.Since(start), cancel, nil
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

func writeUpstreamResponse(w http.ResponseWriter, resp *http.Response, cancel context.CancelFunc) {
	defer closeBody(resp.Body)
	defer cancel()

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	idleTimer := time.NewTimer(upstreamBodyIdleTimeout)
	defer func() {
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
	}()
	stopTimerWatcher := make(chan struct{})
	defer close(stopTimerWatcher)
	go func() {
		select {
		case <-idleTimer.C:
			cancel()
		case <-stopTimerWatcher:
		}
	}()

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			resetTimer(idleTimer, upstreamBodyIdleTimeout)
			if _, err := w.Write(buf[:n]); err != nil {
				log.Printf("[WARN] upstream response write interrupted: %v", err)
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}

		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			return
		}

		log.Printf("[WARN] upstream response read interrupted: %v", readErr)
		return
	}
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

func shouldRetryTransportError(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}

var _ tokenPool = (*pool.TokenPool)(nil)
