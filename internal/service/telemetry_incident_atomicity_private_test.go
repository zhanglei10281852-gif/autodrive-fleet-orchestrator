package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/telemetry"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestCriticalTelemetryFailureDoesNotLeavePartialState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "telemetry-atomicity.db"))
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	region := fleet.Region{ID: "region-telemetry", Code: "TELEMETRY", Name: "Telemetry Test Region", Timezone: "UTC", Status: fleet.RegionActive, MaxVehicles: 10, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	safetyUntil := now.Add(24 * time.Hour)
	vehicle := fleet.Vehicle{ID: "vehicle-telemetry", RegionID: region.ID, VIN: "VIN-TELEMETRY-0001", FleetNumber: "AV-TLM-01", Status: fleet.VehicleAvailable, Capability: "passenger", BatteryPercent: 82, Latitude: 31.20, Longitude: 121.40, SafetyValidUntil: &safetyUntil, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateVehicle(ctx, vehicle); err != nil {
		t.Fatalf("create vehicle: %v", err)
	}

	conflictingOutbox := job.Outbox{ID: "evt_00001501", Topic: "reserved.test.event", AggregateType: "test", AggregateID: "reserved", Payload: json.RawMessage(`{"reserved":true}`), Status: job.StatusPending, MaxAttempts: 2, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	if err := store.Enqueue(ctx, conflictingOutbox); err != nil {
		t.Fatalf("seed conflicting outbox: %v", err)
	}

	operations := NewOperations(store, clock.NewManual(now), idgen.NewSequence(1500), time.Minute)
	critical := telemetry.Sample{EventID: "telemetry-critical-atomic", VehicleID: vehicle.ID, ObservedAt: now.Add(-time.Minute), Latitude: 31.31, Longitude: 121.51, SpeedKPH: 12, BatteryPercent: 39, OdometerMeters: 91000, FaultCode: "BRAKE_PRESSURE", Severity: telemetry.SeverityCritical}
	failed := operations.IngestTelemetry(ctx, []telemetry.Sample{critical})
	if len(failed.Items) != 1 || failed.Items[0].Accepted || failed.Items[0].Code != "persistence_failed" {
		t.Fatalf("outbox conflict should reject the complete telemetry operation: %+v", failed)
	}
	if _, err := store.TelemetryByEventID(ctx, critical.EventID); !errors.Is(err, common.ErrNotFound) {
		t.Errorf("failed critical event left a telemetry row: %v", err)
	}
	afterFailure, err := store.VehicleByID(ctx, vehicle.ID)
	if err != nil {
		t.Fatalf("load vehicle after failure: %v", err)
	}
	if afterFailure.BatteryPercent != vehicle.BatteryPercent || afterFailure.Latitude != vehicle.Latitude || afterFailure.Longitude != vehicle.Longitude || afterFailure.LastTelemetryAt != nil || afterFailure.Version != vehicle.Version {
		t.Errorf("failed critical event changed the vehicle snapshot: %+v", afterFailure)
	}
	if _, err := store.IncidentByID(ctx, "inc_00001500"); !errors.Is(err, common.ErrNotFound) {
		t.Errorf("failed critical event left an incident: %v", err)
	}
	if _, err := store.AuditEventByID(ctx, "aud_00001502"); !errors.Is(err, common.ErrNotFound) {
		t.Errorf("failed critical event left an audit record: %v", err)
	}
	reserved, err := store.OutboxByID(ctx, conflictingOutbox.ID)
	if err != nil || reserved.Topic != conflictingOutbox.Topic || string(reserved.Payload) != string(conflictingOutbox.Payload) {
		t.Errorf("outbox conflict row was overwritten: %+v err=%v", reserved, err)
	}

	retried := operations.IngestTelemetry(ctx, []telemetry.Sample{critical})
	if len(retried.Items) != 1 || !retried.Items[0].Accepted || retried.Items[0].Duplicate {
		t.Fatalf("retry after a fully rolled-back failure should persist the event and incident: %+v", retried)
	}
	if _, err := store.TelemetryByEventID(ctx, critical.EventID); err != nil {
		t.Errorf("retry did not persist telemetry: %v", err)
	}
	incident, err := store.IncidentByID(ctx, "inc_00001503")
	if err != nil || incident.TelemetryEvent != critical.EventID || incident.VehicleID != vehicle.ID {
		t.Errorf("retry did not create the linked safety incident: %+v err=%v", incident, err)
	}
	if notification, err := store.OutboxByID(ctx, "evt_00001504"); err != nil || notification.AggregateID != "inc_00001503" {
		t.Errorf("retry did not enqueue the incident notification: %+v err=%v", notification, err)
	}
	if event, err := store.AuditEventByID(ctx, "aud_00001505"); err != nil || event.ObjectID != "inc_00001503" || event.Action != "safety.incident.open" {
		t.Errorf("retry did not record incident audit: %+v err=%v", event, err)
	}

	normal := telemetry.Sample{EventID: "telemetry-critical-normal", VehicleID: vehicle.ID, ObservedAt: now, Latitude: 31.32, Longitude: 121.52, SpeedKPH: 8, BatteryPercent: 37, OdometerMeters: 91010, FaultCode: "STEERING_SENSOR", Severity: telemetry.SeverityCritical}
	succeeded := operations.IngestTelemetry(ctx, []telemetry.Sample{normal})
	if len(succeeded.Items) != 1 || !succeeded.Items[0].Accepted || succeeded.Items[0].Duplicate {
		t.Fatalf("nonconflicting critical telemetry should succeed: %+v", succeeded)
	}
	if incident, err := store.IncidentByID(ctx, "inc_00001506"); err != nil || incident.TelemetryEvent != normal.EventID {
		t.Errorf("normal critical telemetry lacks incident: %+v err=%v", incident, err)
	}
	if notification, err := store.OutboxByID(ctx, "evt_00001507"); err != nil || notification.AggregateID != "inc_00001506" {
		t.Errorf("normal critical telemetry lacks notification: %+v err=%v", notification, err)
	}
	if event, err := store.AuditEventByID(ctx, "aud_00001508"); err != nil || event.ObjectID != "inc_00001506" {
		t.Errorf("normal critical telemetry lacks audit: %+v err=%v", event, err)
	}
}
