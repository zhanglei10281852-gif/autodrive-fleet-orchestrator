package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address           string
	DatabasePath      string
	LogLevel          string
	SessionTTL        time.Duration
	WorkerInterval    time.Duration
	WorkerLease       time.Duration
	ShutdownTimeout   time.Duration
	BootstrapAdmin    string
	BootstrapPassword string
	MaxRequestBytes   int64
}

func Load() (Config, error) {
	cfg := Config{
		Address:           env("AUTODRIVE_ADDR", ":8080"),
		DatabasePath:      env("AUTODRIVE_DB_PATH", "./data/autodrive.db"),
		LogLevel:          env("AUTODRIVE_LOG_LEVEL", "info"),
		BootstrapAdmin:    env("AUTODRIVE_BOOTSTRAP_ADMIN", "admin"),
		BootstrapPassword: env("AUTODRIVE_BOOTSTRAP_PASSWORD", "change-me-now"),
		MaxRequestBytes:   1 << 20,
	}
	var err error
	if cfg.SessionTTL, err = duration("AUTODRIVE_SESSION_TTL", 8*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.WorkerInterval, err = duration("AUTODRIVE_WORKER_INTERVAL", 2*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.WorkerLease, err = duration("AUTODRIVE_WORKER_LEASE", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = duration("AUTODRIVE_SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if raw := os.Getenv("AUTODRIVE_MAX_REQUEST_BYTES"); raw != "" {
		cfg.MaxRequestBytes, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cfg.MaxRequestBytes < 1024 {
			return Config{}, fmt.Errorf("AUTODRIVE_MAX_REQUEST_BYTES must be an integer >= 1024")
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string
	if strings.TrimSpace(c.Address) == "" {
		problems = append(problems, "address is required")
	}
	if strings.TrimSpace(c.DatabasePath) == "" {
		problems = append(problems, "database path is required")
	}
	if c.SessionTTL <= 0 {
		problems = append(problems, "session ttl must be positive")
	}
	if c.WorkerInterval <= 0 || c.WorkerLease <= c.WorkerInterval {
		problems = append(problems, "worker lease must exceed the worker interval")
	}
	if len(c.BootstrapPassword) < 10 {
		problems = append(problems, "bootstrap password must contain at least 10 characters")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}
