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
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestOpenMaintenanceFailureReleasesVehicleOwnership(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 6, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "maintenance-open.db"))
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	operator := auth.User{ID: "maintenance-operator", Username: "safety-maintenance", PasswordHash: "password-hash", Role: auth.RoleSafetyOperator, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateUser(ctx, operator); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	region := fleet.Region{ID: "maintenance-region", Code: "MAINT", Name: "Maintenance Zone", Timezone: "UTC", Status: fleet.RegionActive, MaxVehicles: 10, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	createVehicle := func(id, number string) fleet.Vehicle {
		vehicle := fleet.Vehicle{ID: id, RegionID: region.ID, VIN: "VIN-" + number + "-2026", FleetNumber: number, Status: fleet.VehicleAvailable, Capability: "urban", BatteryPercent: 80, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateVehicle(ctx, vehicle); err != nil {
			t.Fatalf("create vehicle %s: %v", id, err)
		}
		return vehicle
	}
	conflicted := createVehicle("vehicle-maintenance-conflict", "MNT-21-A")
	legal := createVehicle("vehicle-maintenance-legal", "MNT-21-B")
	if err := store.AppendAudit(ctx, audit.Event{ID: "aud_00002101", ActorID: operator.ID, ActorRole: string(operator.Role), Action: "fixture.reserve", ObjectType: "vehicle", ObjectID: conflicted.ID, Result: audit.ResultSuccess, RequestID: "occupied-maintenance-audit", Details: []byte("{}"), CreatedAt: now}); err != nil {
		t.Fatalf("reserve generated audit ID: %v", err)
	}

	resourceService := NewResource(store, clock.NewManual(now), idgen.NewSequence(2100))
	principal := auth.Principal{UserID: operator.ID, Username: operator.Username, Role: operator.Role, SessionID: "maintenance-session"}
	failedCtx := request.WithID(ctx, "maintenance-open-conflict")
	_, err = resourceService.OpenMaintenance(failedCtx, principal, OpenMaintenanceInput{VehicleID: conflicted.ID, Reason: "steering vibration", Priority: "high", RequiredChecks: []string{"steering", "brakes"}})
	if !errors.Is(err, common.ErrConflict) {
		t.Fatalf("maintenance audit collision error = %v, want conflict", err)
	}
	storedVehicle, err := store.VehicleByID(ctx, conflicted.ID)
	if err != nil {
		t.Fatalf("load vehicle after failed opening: %v", err)
	}
	if storedVehicle.Status != fleet.VehicleAvailable || storedVehicle.Version != conflicted.Version {
		t.Fatalf("failed maintenance opening retained vehicle ownership: %+v", storedVehicle)
	}
	if _, err := store.MaintenanceByID(ctx, "mnt_00002100"); !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("failed opening work order lookup error = %v, want not found", err)
	}
	failedAudits, err := store.AuditCountForRequest(ctx, "maintenance-open-conflict")
	if err != nil {
		t.Fatalf("count failed opening audits: %v", err)
	}
	if failedAudits != 0 {
		t.Fatalf("failed maintenance opening left audit count=%d, want 0", failedAudits)
	}

	legalCtx := request.WithID(ctx, "maintenance-open-legal")
	opened, err := resourceService.OpenMaintenance(legalCtx, principal, OpenMaintenanceInput{VehicleID: legal.ID, Reason: "scheduled lidar calibration", Priority: "medium", RequiredChecks: []string{"lidar"}})
	if err != nil {
		t.Fatalf("legal maintenance opening failed: %v", err)
	}
	legalVehicle, err := store.VehicleByID(ctx, legal.ID)
	if err != nil {
		t.Fatalf("load legal maintenance vehicle: %v", err)
	}
	legalAudits, err := store.AuditCountForRequest(ctx, "maintenance-open-legal")
	if err != nil {
		t.Fatalf("count legal opening audit: %v", err)
	}
	if opened.ID != "mnt_00002102" || legalVehicle.Status != fleet.VehicleMaintenance || legalAudits != 1 {
		t.Fatalf("legal opening did not commit order, ownership, and audit: order=%+v vehicle=%+v audits=%d", opened, legalVehicle, legalAudits)
	}
}
