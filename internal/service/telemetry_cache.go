package service

import (
	"sync"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/telemetry"
)

type telemetryCacheKey struct {
	vehicleID string
	since     time.Time
	limit     int
}

type recentTelemetryCache struct {
	mu      sync.RWMutex
	entries map[telemetryCacheKey][]telemetry.Sample
}

func newRecentTelemetryCache() *recentTelemetryCache {
	return &recentTelemetryCache{entries: make(map[telemetryCacheKey][]telemetry.Sample)}
}

func (c *recentTelemetryCache) get(key telemetryCacheKey) ([]telemetry.Sample, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	samples, ok := c.entries[key]
	return samples, ok
}

func (c *recentTelemetryCache) put(key telemetryCacheKey, samples []telemetry.Sample) {
	c.mu.Lock()
	c.entries[key] = samples
	c.mu.Unlock()
}
