package proxy

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTokenPool struct {
	mu             sync.Mutex
	tokens         []string
	cooldownMarked []string
	index          int
}

func (p *fakeTokenPool) PickExcluding(excluded map[string]struct{}) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for offset := range p.tokens {
		idx := (p.index + offset) % len(p.tokens)
		token := p.tokens[idx]
		if _, skip := excluded[token]; skip {
			continue
		}
		p.index = (idx + 1) % len(p.tokens)
		return token, nil
	}

	return "", errNoTokenAvailableForTest{}
}

func (p *fakeTokenPool) MarkCooldown(token string, duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cooldownMarked = append(p.cooldownMarked, token)
}

func (p *fakeTokenPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.tokens)
}

func (p *fakeTokenPool) CooldownMarked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.cooldownMarked))
	copy(out, p.cooldownMarked)
	return out
}

type errNoTokenAvailableForTest struct{}

func (errNoTokenAvailableForTest) Error() string { return "no token available" }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func TestServeHTTPForwardsRequestAndOverridesAuthorization(t *testing.T) {
	requests := make(chan *http.Request, 1)
	bodies := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- r.Clone(r.Context())
		bodies <- string(body)
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	baseURL, err := url.Parse(upstream.URL + "/v1")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	pool := &fakeTokenPool{tokens: []string{"token-a"}}
	handler := NewHandler(baseURL, pool, time.Minute, 10*1024*1024)

	req := httptest.NewRequest(http.MethodPost, "/chat/completions?stream=true", strings.NewReader(`{"model":"x"}`))
	req.Header.Set("Authorization", "Bearer client-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}
	if rec.Header().Get("X-Upstream") != "ok" {
		t.Fatalf("expected upstream header to be copied")
	}
	if body := rec.Body.String(); body != `{"ok":true}` {
		t.Fatalf("unexpected response body: %q", body)
	}

	upstreamReq := <-requests
	if upstreamReq.URL.Path != "/v1/chat/completions" {
		t.Fatalf("expected upstream path /v1/chat/completions, got %q", upstreamReq.URL.Path)
	}
	if upstreamReq.URL.RawQuery != "stream=true" {
		t.Fatalf("expected query string to be preserved, got %q", upstreamReq.URL.RawQuery)
	}
	if got := upstreamReq.Header.Get("Authorization"); got != "Bearer token-a" {
		t.Fatalf("expected upstream authorization to use pool token, got %q", got)
	}
	if got := <-bodies; got != `{"model":"x"}` {
		t.Fatalf("unexpected upstream body: %q", got)
	}
}

func TestServeHTTPRetriesOn429AndMarksCooldown(t *testing.T) {
	var mu sync.Mutex
	seenTokens := []string{}
	seenBodies := []string{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seenTokens = append(seenTokens, r.Header.Get("Authorization"))
		seenBodies = append(seenBodies, string(body))
		call := len(seenTokens)
		mu.Unlock()

		if call == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	baseURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	pool := &fakeTokenPool{tokens: []string{"token-a", "token-b"}}
	handler := NewHandler(baseURL, pool, 2*time.Minute, 10*1024*1024)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hi"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if got := rec.Body.String(); got != `{"ok":true}` {
		t.Fatalf("unexpected response body: %q", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenTokens) != 2 {
		t.Fatalf("expected 2 upstream attempts, got %d", len(seenTokens))
	}
	if seenTokens[0] != "Bearer token-a" || seenTokens[1] != "Bearer token-b" {
		t.Fatalf("unexpected retry token order: %#v", seenTokens)
	}
	if seenBodies[0] != `{"input":"hi"}` || seenBodies[1] != `{"input":"hi"}` {
		t.Fatalf("expected request body to be preserved across retry, got %#v", seenBodies)
	}
	if marked := pool.CooldownMarked(); len(marked) != 1 || marked[0] != "token-a" {
		t.Fatalf("expected token-a cooldown mark, got %#v", marked)
	}
}

func TestServeHTTPRetriesOnceOnTransportError(t *testing.T) {
	baseURL, err := url.Parse("https://example.com/v1")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	pool := &fakeTokenPool{tokens: []string{"token-a", "token-b"}}
	handler := NewHandler(baseURL, pool, time.Minute, 10*1024*1024)

	var mu sync.Mutex
	seenTokens := []string{}
	seenBodies := []string{}
	handler.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seenTokens = append(seenTokens, r.Header.Get("Authorization"))
		seenBodies = append(seenBodies, string(body))
		call := len(seenTokens)
		mu.Unlock()

		if call == 1 {
			return nil, errors.New("dial tcp: i/o timeout")
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})

	req := httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"x"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seenTokens) != 2 {
		t.Fatalf("expected 2 transport attempts, got %d", len(seenTokens))
	}
	if seenTokens[0] != "Bearer token-a" || seenTokens[1] != "Bearer token-b" {
		t.Fatalf("unexpected token order: %#v", seenTokens)
	}
	if seenBodies[0] != `{"model":"x"}` || seenBodies[1] != `{"model":"x"}` {
		t.Fatalf("expected body to be preserved across transport retry, got %#v", seenBodies)
	}
}

func TestServeHTTPRetriesOnceOnServerError(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"bad gateway"}`))
			return
		}

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	baseURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	pool := &fakeTokenPool{tokens: []string{"token-a", "token-b", "token-c"}}
	handler := NewHandler(baseURL, pool, time.Minute, 10*1024*1024)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}
	if calls != 2 {
		t.Fatalf("expected exactly 2 upstream calls, got %d", calls)
	}
}

func TestServeHTTPReturns503WhenNoTokenAvailable(t *testing.T) {
	baseURL, err := url.Parse("https://example.com")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	pool := &fakeTokenPool{}
	handler := NewHandler(baseURL, pool, time.Minute, 10*1024*1024)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"all tokens are rate limited, please retry later"}` {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestServeHTTPRejectsTooLargeRequestBody(t *testing.T) {
	baseURL, err := url.Parse("https://example.com")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	pool := &fakeTokenPool{tokens: []string{"token-a"}}
	handler := NewHandler(baseURL, pool, time.Minute, 1024)

	body := strings.Repeat("a", 1025)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status %d, got %d", http.StatusRequestEntityTooLarge, rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"request body too large"}` {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestServeHTTPAllowsRequestBodyUpToConfiguredLimit(t *testing.T) {
	requests := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	baseURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	bodyLimit := int64(2 * 1024 * 1024)
	pool := &fakeTokenPool{tokens: []string{"token-a"}}
	handler := NewHandler(baseURL, pool, time.Minute, bodyLimit)

	body := strings.Repeat("a", int(bodyLimit))
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	select {
	case <-requests:
	default:
		t.Fatal("expected request to reach upstream when body is at configured limit")
	}
}

func TestNewHandlerUsesStreamingFriendlyHTTPClient(t *testing.T) {
	baseURL, err := url.Parse("https://example.com/v1")
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}

	handler := NewHandler(baseURL, &fakeTokenPool{tokens: []string{"token-a"}}, time.Minute, 10*1024*1024)

	if handler.client.Timeout != 0 {
		t.Fatalf("expected client timeout 0 for streaming safety, got %s", handler.client.Timeout)
	}

	transport, ok := handler.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", handler.client.Transport)
	}
	if transport.ResponseHeaderTimeout <= 0 {
		t.Fatal("expected ResponseHeaderTimeout to be set")
	}
	if transport.TLSHandshakeTimeout <= 0 {
		t.Fatal("expected TLSHandshakeTimeout to be set")
	}
	if transport.IdleConnTimeout <= 0 {
		t.Fatal("expected IdleConnTimeout to be set")
	}
}

func TestJoinPathKeepsBasePathWithoutDuplication(t *testing.T) {
	cases := []struct {
		basePath    string
		requestPath string
		want        string
	}{
		{basePath: "/v1", requestPath: "/chat/completions", want: "/v1/chat/completions"},
		{basePath: "/v1", requestPath: "/v1/models", want: "/v1/models"},
		{basePath: "/v1/", requestPath: "/v1/responses", want: "/v1/responses"},
		{basePath: "", requestPath: "/models", want: "/models"},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s_%s", tc.basePath, tc.requestPath), func(t *testing.T) {
			if got := joinPath(tc.basePath, tc.requestPath); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}
