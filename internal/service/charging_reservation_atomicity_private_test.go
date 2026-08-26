package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/charging"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestChargingReservationAuditFailureDoesNotConsumeWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 7, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "charging-reservation.db"))
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	operator := auth.User{ID: "charging-dispatcher", Username: "charging-dispatcher", PasswordHash: "password-hash", Role: auth.RoleDispatcher, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateUser(ctx, operator); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	region := fleet.Region{ID: "charging-region", Code: "CHARGE", Name: "Charging Zone", Timezone: "UTC", Status: fleet.RegionActive, MaxVehicles: 10, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	vehicle := fleet.Vehicle{ID: "charging-vehicle", RegionID: region.ID, VIN: "VIN-CHARGING-2200", FleetNumber: "CHG-22", Status: fleet.VehicleAvailable, Capability: "urban", BatteryPercent: 25, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateVehicle(ctx, vehicle); err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	station := charging.Station{ID: "station-22", RegionID: region.ID, Code: "ST-22", Name: "Depot Charger", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateStation(ctx, station); err != nil {
		t.Fatalf("create station: %v", err)
	}
	connector := charging.Connector{ID: "connector-22", StationID: station.ID, Code: "CON-22", PowerKW: 120, Active: true, Version: 1}
	if err := store.CreateConnector(ctx, connector); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	if err := store.AppendAudit(ctx, audit.Event{ID: "aud_00002201", ActorID: operator.ID, ActorRole: string(operator.Role), Action: "fixture.reserve", ObjectType: "connector", ObjectID: connector.ID, Result: audit.ResultSuccess, RequestID: "occupied-charging-audit", Details: []byte("{}"), CreatedAt: now}); err != nil {
		t.Fatalf("reserve generated audit ID: %v", err)
	}

	resourceService := NewResource(store, clock.NewManual(now), idgen.NewSequence(2200))
	principal := auth.Principal{UserID: operator.ID, Username: operator.Username, Role: operator.Role, SessionID: "charging-session"}
	input := ReserveChargingInput{VehicleID: vehicle.ID, ConnectorID: connector.ID, WindowStart: now.Add(time.Hour), WindowEnd: now.Add(2 * time.Hour), IdempotencyKey: "charging-reservation-22"}
	_, err = resourceService.ReserveCharging(request.WithID(ctx, "charging-reserve-conflict"), principal, input)
	if !errors.Is(err, common.ErrConflict) {
		t.Fatalf("reservation audit collision error = %v, want conflict", err)
	}
	if _, err := store.ChargingSessionByID(ctx, "chg_00002200"); !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("failed reservation consumed connector window: %v", err)
	}
	failedAudits, err := store.AuditCountForRequest(ctx, "charging-reserve-conflict")
	if err != nil {
		t.Fatalf("count failed reservation audits: %v", err)
	}
	if failedAudits != 0 {
		t.Fatalf("failed reservation left audit count=%d, want 0", failedAudits)
	}

	retried, err := resourceService.ReserveCharging(request.WithID(ctx, "charging-reserve-retry"), principal, input)
	if err != nil {
		t.Fatalf("retry after rolled-back reservation failed: %v", err)
	}
	retryAudits, err := store.AuditCountForRequest(ctx, "charging-reserve-retry")
	if err != nil {
		t.Fatalf("count retry audit: %v", err)
	}
	if retried.ID != "chg_00002202" || retryAudits != 1 {
		t.Fatalf("retry did not create a fresh reservation and audit: reservation=%+v audits=%d", retried, retryAudits)
	}
}
