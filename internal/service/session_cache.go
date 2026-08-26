package service

import (
	"sync"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
)

type cachedSessionPrincipal struct {
	principal auth.Principal
	expiresAt time.Time
}

type sessionPrincipalCache struct {
	mu      sync.RWMutex
	entries map[string]cachedSessionPrincipal
}

func newSessionPrincipalCache() *sessionPrincipalCache {
	return &sessionPrincipalCache{entries: make(map[string]cachedSessionPrincipal)}
}

func (c *sessionPrincipalCache) Get(tokenHash string, now time.Time) (auth.Principal, bool) {
	c.mu.RLock()
	entry, ok := c.entries[tokenHash]
	c.mu.RUnlock()
	if !ok {
		return auth.Principal{}, false
	}
	if !entry.expiresAt.After(now) {
		c.mu.Lock()
		if current, exists := c.entries[tokenHash]; exists && !current.expiresAt.After(now) {
			delete(c.entries, tokenHash)
		}
		c.mu.Unlock()
		return auth.Principal{}, false
	}
	return entry.principal, true
}

func (c *sessionPrincipalCache) Put(tokenHash string, principal auth.Principal, expiresAt time.Time) {
	c.mu.Lock()
	c.entries[tokenHash] = cachedSessionPrincipal{principal: principal, expiresAt: expiresAt}
	c.mu.Unlock()
}
