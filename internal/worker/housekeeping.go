package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
)

type HousekeepingRepository interface {
	DeleteExpiredSessions(context.Context, time.Time, int) (int64, error)
	ExpireChargingReservations(context.Context, time.Time, int) (int64, error)
	DeleteTelemetryBefore(context.Context, time.Time, int) (int64, error)
}

type Housekeeping struct {
	repository HousekeepingRepository
	clock      clock.Clock
	logger     *slog.Logger
	interval   time.Duration
	retention  time.Duration
}

func NewHousekeeping(repository HousekeepingRepository, businessClock clock.Clock, logger *slog.Logger, interval, retention time.Duration) *Housekeeping {
	return &Housekeeping{repository: repository, clock: businessClock, logger: logger, interval: interval, retention: retention}
}

func (w *Housekeeping) Run(ctx context.Context) error {
	if w.interval <= 0 || w.retention <= 0 {
		return errors.New("invalid housekeeping configuration")
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.ProcessOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.ErrorContext(ctx, "housekeeping cycle failed", "error", err)
			}
		}
	}
}

func (w *Housekeeping) ProcessOnce(ctx context.Context) error {
	now := w.clock.Now()
	deletedSessions, err := w.repository.DeleteExpiredSessions(ctx, now, 500)
	if err != nil {
		return err
	}
	expiredReservations, err := w.repository.ExpireChargingReservations(ctx, now, 500)
	if err != nil {
		return err
	}
	deletedTelemetry, err := w.repository.DeleteTelemetryBefore(ctx, now.Add(-w.retention), 1000)
	if err != nil {
		return err
	}
	if deletedSessions+expiredReservations+deletedTelemetry > 0 {
		w.logger.InfoContext(ctx, "housekeeping completed", "sessions", deletedSessions,
			"reservations", expiredReservations, "telemetry", deletedTelemetry)
	}
	return nil
}
