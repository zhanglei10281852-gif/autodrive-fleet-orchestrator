package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/charging"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestChargingStartConflictLeavesAllOwnershipUnchanged(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "charging-start.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	stamp := now.Format(time.RFC3339Nano)
	windowStart := now.Add(-10 * time.Minute).Format(time.RFC3339Nano)
	windowEnd := now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	leaseUntil := now.Add(time.Hour).Format(time.RFC3339Nano)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id, username, password_hash, role, active, created_at, updated_at) VALUES(?, ?, ?, ?, 1, ?, ?)`, []any{"usr_dispatch", "dispatch-start", "hash", "dispatcher", stamp, stamp}},
		{`INSERT INTO regions(id, code, name, timezone, status, max_vehicles, version, created_at, updated_at) VALUES(?, ?, ?, ?, 'active', 10, 1, ?, ?)`, []any{"reg_start", "START", "Start Region", "UTC", stamp, stamp}},
		{`INSERT INTO charging_stations(id, region_id, code, name, active, created_at, updated_at) VALUES(?, ?, ?, ?, 1, ?, ?)`, []any{"stn_start", "reg_start", "START-1", "Start Station", stamp, stamp}},
		{`INSERT INTO vehicles(id, region_id, vin, fleet_number, status, capability, battery_percent, latitude, longitude, version, created_at, updated_at) VALUES(?, ?, ?, ?, 'available', 'passenger', 40, 0, 0, 1, ?, ?)`, []any{"veh_conflict", "reg_start", "VIN-START-CONFLICT", "FLEET-START-CONFLICT", stamp, stamp}},
		{`INSERT INTO vehicles(id, region_id, vin, fleet_number, status, capability, battery_percent, latitude, longitude, version, created_at, updated_at) VALUES(?, ?, ?, ?, 'available', 'passenger', 55, 0, 0, 1, ?, ?)`, []any{"veh_success", "reg_start", "VIN-START-SUCCESS", "FLEET-START-SUCCESS", stamp, stamp}},
		{`INSERT INTO charging_connectors(id, station_id, code, power_kw, active, version, lease_owner_id, lease_until) VALUES(?, ?, ?, 120, 1, 1, ?, ?)`, []any{"con_conflict", "stn_start", "CONFLICT", "another-active-session", leaseUntil}},
		{`INSERT INTO charging_connectors(id, station_id, code, power_kw, active, version, lease_owner_id, lease_until) VALUES(?, ?, ?, 120, 1, 1, '', NULL)`, []any{"con_success", "stn_start", "SUCCESS"}},
		{`INSERT INTO charging_sessions(id, vehicle_id, connector_id, status, window_start, window_end, initial_battery, energy_watt_hours, idempotency_key, version, created_by, created_at) VALUES(?, ?, ?, 'reserved', ?, ?, 40, 0, ?, 1, ?, ?)`, []any{"chg_conflict", "veh_conflict", "con_conflict", windowStart, windowEnd, "idem-conflict", "usr_dispatch", stamp}},
		{`INSERT INTO charging_sessions(id, vehicle_id, connector_id, status, window_start, window_end, initial_battery, energy_watt_hours, idempotency_key, version, created_by, created_at) VALUES(?, ?, ?, 'reserved', ?, ?, 55, 0, ?, 1, ?, ?)`, []any{"chg_success", "veh_success", "con_success", windowStart, windowEnd, "idem-success", "usr_dispatch", stamp}},
	}
	for _, statement := range statements {
		if _, err := store.DB().ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed charging scenario: %v", err)
		}
	}

	resources := service.NewResource(store, clock.NewManual(now), idgen.NewSequence(700))
	principal := auth.Principal{UserID: "usr_dispatch", Username: "dispatch-start", Role: auth.RoleDispatcher, SessionID: "ses_start"}
	if _, err := resources.StartCharging(ctx, principal, "chg_conflict", 1); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("start with foreign connector lease returned %v, want conflict", err)
	}
	failedSession, err := store.ChargingSessionByID(ctx, "chg_conflict")
	if err != nil {
		t.Fatalf("read failed session: %v", err)
	}
	if failedSession.Status != charging.StatusReserved || failedSession.Version != 1 || failedSession.StartedAt != nil {
		t.Fatalf("failed start changed session: %+v", failedSession)
	}
	failedVehicle, err := store.VehicleByID(ctx, "veh_conflict")
	if err != nil {
		t.Fatalf("read failed-start vehicle: %v", err)
	}
	if failedVehicle.Status != fleet.VehicleAvailable || failedVehicle.Version != 1 {
		t.Fatalf("failed start changed vehicle: %+v", failedVehicle)
	}
	foreignConnector, err := store.ConnectorByID(ctx, "con_conflict")
	if err != nil {
		t.Fatalf("read conflicting connector: %v", err)
	}
	if foreignConnector.LeaseOwnerID != "another-active-session" || foreignConnector.Version != 1 {
		t.Fatalf("failed start changed foreign connector lease: %+v", foreignConnector)
	}

	started, err := resources.StartCharging(ctx, principal, "chg_success", 1)
	if err != nil {
		t.Fatalf("uncontended start failed: %v", err)
	}
	if started.Status != charging.StatusActive || started.Version != 2 {
		t.Fatalf("uncontended start returned wrong session: %+v", started)
	}
	persisted, err := store.ChargingSessionByID(ctx, "chg_success")
	if err != nil || persisted.Status != charging.StatusActive || persisted.StartedAt == nil {
		t.Fatalf("successful session was not activated: session=%+v err=%v", persisted, err)
	}
	successVehicle, err := store.VehicleByID(ctx, "veh_success")
	if err != nil || successVehicle.Status != fleet.VehicleCharging || successVehicle.Version != 2 {
		t.Fatalf("successful start did not acquire vehicle: vehicle=%+v err=%v", successVehicle, err)
	}
	successConnector, err := store.ConnectorByID(ctx, "con_success")
	if err != nil || successConnector.LeaseOwnerID != "chg_success" || successConnector.Version != 2 {
		t.Fatalf("successful start did not acquire connector: connector=%+v err=%v", successConnector, err)
	}
}
