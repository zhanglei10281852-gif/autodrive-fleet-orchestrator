package service

import (
	"context"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/safety"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/telemetry"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/repository"
)

type telemetryCacheRepository struct {
	samples []telemetry.Sample
	calls   []telemetryCacheKey
}

func (r *telemetryCacheRepository) RecentTelemetry(_ context.Context, vehicleID string, since time.Time, limit int) ([]telemetry.Sample, error) {
	r.calls = append(r.calls, telemetryCacheKey{vehicleID: vehicleID, since: since.UTC(), limit: limit})
	return append([]telemetry.Sample(nil), r.samples...), nil
}

func (*telemetryCacheRepository) VehicleByID(context.Context, string) (fleet.Vehicle, error) {
	return fleet.Vehicle{}, common.ErrNotFound
}
func (*telemetryCacheRepository) CommitTelemetry(context.Context, repository.TelemetryCommit) (bool, error) {
	return false, nil
}
func (*telemetryCacheRepository) IncidentByID(context.Context, string) (safety.Incident, error) {
	return safety.Incident{}, common.ErrNotFound
}
func (*telemetryCacheRepository) ListIncidents(context.Context, safety.Filter) (common.Page[safety.Incident], error) {
	return common.Page[safety.Incident]{}, nil
}
func (*telemetryCacheRepository) ClaimIncident(context.Context, string, string, time.Time, time.Duration, int64) (safety.Incident, error) {
	return safety.Incident{}, common.ErrNotFound
}
func (*telemetryCacheRepository) UpdateIncident(context.Context, safety.Incident, int64) error {
	return nil
}
func (*telemetryCacheRepository) CommitIncidentResolution(context.Context, repository.IncidentResolutionCommit) error {
	return nil
}

func TestRecentTelemetryCacheDoesNotShareCallerSlice(t *testing.T) {
	now := time.Date(2026, 8, 26, 19, 0, 0, 0, time.UTC)
	since := now.Add(-time.Hour)
	repository := &telemetryCacheRepository{samples: []telemetry.Sample{
		{EventID: "telemetry-new", VehicleID: "vehicle-cache", ObservedAt: now.Add(-time.Minute), BatteryPercent: 64},
		{EventID: "telemetry-old", VehicleID: "vehicle-cache", ObservedAt: now.Add(-2 * time.Minute), BatteryPercent: 65},
	}}
	service := NewOperations(repository, clock.NewManual(now), idgen.NewSequence(1), time.Minute)
	principal := auth.Principal{UserID: "dispatcher-cache", Username: "dispatcher-cache", Role: auth.RoleDispatcher, SessionID: "session-cache"}

	first, err := service.RecentTelemetry(context.Background(), principal, "vehicle-cache", since, 10)
	if err != nil {
		t.Fatalf("first recent telemetry read: %v", err)
	}
	if len(first) != 2 || first[0].EventID != "telemetry-new" || first[1].EventID != "telemetry-old" {
		t.Fatalf("unexpected first telemetry result: %+v", first)
	}
	first[0], first[1] = first[1], first[0]
	first[0].BatteryPercent = 1

	second, err := service.RecentTelemetry(context.Background(), principal, "vehicle-cache", since, 10)
	if err != nil {
		t.Fatalf("cached recent telemetry read: %v", err)
	}
	if len(second) != 2 || second[0].EventID != "telemetry-new" || second[0].BatteryPercent != 64 || second[1].EventID != "telemetry-old" {
		t.Fatalf("caller mutation polluted cached telemetry: %+v", second)
	}
	if len(repository.calls) != 1 {
		t.Fatalf("same telemetry window repository calls=%d want=1", len(repository.calls))
	}

	if _, err := service.RecentTelemetry(context.Background(), principal, "vehicle-cache", since.Add(time.Minute), 10); err != nil {
		t.Fatalf("different telemetry window read: %v", err)
	}
	if len(repository.calls) != 2 {
		t.Fatalf("different telemetry window reused cache entry: calls=%d", len(repository.calls))
	}
}

var _ OperationsRepository = (*telemetryCacheRepository)(nil)
