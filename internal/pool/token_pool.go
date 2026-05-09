package pool

import (
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrNoTokenAvailable = errors.New("all tokens are cooling down")

type TokenPool struct {
	mu     sync.Mutex
	tokens []tokenEntry
	index  int
}

type tokenEntry struct {
	value     string
	coolUntil time.Time
}

func NewTokenPool(tokens []string) *TokenPool {
	entries := make([]tokenEntry, 0, len(tokens))
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}

		entries = append(entries, tokenEntry{value: trimmed})
	}

	return &TokenPool{tokens: entries}
}

func (p *TokenPool) Pick() (string, error) {
	return p.PickExcluding(nil)
}

func (p *TokenPool) PickExcluding(excluded map[string]struct{}) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.tokens) == 0 {
		return "", ErrNoTokenAvailable
	}

	now := time.Now()
	for offset := range p.tokens {
		idx := (p.index + offset) % len(p.tokens)
		entry := p.tokens[idx]

		if _, skip := excluded[entry.value]; skip {
			continue
		}

		if entry.coolUntil.After(now) {
			continue
		}

		p.index = (idx + 1) % len(p.tokens)
		return entry.value, nil
	}

	return "", ErrNoTokenAvailable
}

func (p *TokenPool) MarkCooldown(token string, duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for idx := range p.tokens {
		if p.tokens[idx].value == token {
			p.tokens[idx].coolUntil = time.Now().Add(duration)
			return
		}
	}
}

func (p *TokenPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.tokens)
}
