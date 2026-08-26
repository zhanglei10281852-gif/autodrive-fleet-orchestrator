package worker

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
)

type cancellationAwareHousekeepingRepository struct {
	calls []string
}

func (r *cancellationAwareHousekeepingRepository) record(ctx context.Context, name string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	r.calls = append(r.calls, name)
	return 1, nil
}

func (r *cancellationAwareHousekeepingRepository) DeleteExpiredSessions(ctx context.Context, _ time.Time, _ int) (int64, error) {
	return r.record(ctx, "sessions")
}

func (r *cancellationAwareHousekeepingRepository) ExpireChargingReservations(ctx context.Context, _ time.Time, _ int) (int64, error) {
	return r.record(ctx, "reservations")
}

func (r *cancellationAwareHousekeepingRepository) DeleteTelemetryBefore(ctx context.Context, _ time.Time, _ int) (int64, error) {
	return r.record(ctx, "telemetry")
}

func TestCancelledHousekeepingCyclePerformsNoRetentionWrites(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	repository := &cancellationAwareHousekeepingRepository{}
	worker := NewHousekeeping(repository, clock.NewManual(now), workerLogger(), time.Minute, 30*24*time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.ProcessOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cycle error=%v", err)
	}
	if len(repository.calls) != 0 {
		t.Fatalf("cancelled cycle executed retention writes: %v", repository.calls)
	}

	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("active cycle: %v", err)
	}
	want := []string{"sessions", "reservations", "telemetry"}
	if !slices.Equal(repository.calls, want) {
		t.Fatalf("active cycle calls=%v want=%v", repository.calls, want)
	}
}
