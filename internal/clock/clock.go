package clock

import (
	"sync"
	"time"
)

// Clock makes business time explicit without leaking test controls into services.
type Clock interface {
	Now() time.Time
}

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

type Manual struct {
	mu  sync.RWMutex
	now time.Time
}

func NewManual(at time.Time) *Manual {
	return &Manual{now: at.UTC()}
}

func (m *Manual) Now() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.now
}

func (m *Manual) Set(at time.Time) {
	m.mu.Lock()
	m.now = at.UTC()
	m.mu.Unlock()
}

func (m *Manual) Advance(d time.Duration) {
	m.mu.Lock()
	m.now = m.now.Add(d)
	m.mu.Unlock()
}
