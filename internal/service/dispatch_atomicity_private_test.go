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
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/trip"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestDispatchAuditFailureRollsBackMissionAssignment(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "dispatch.db"))
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	principal := auth.Principal{UserID: "dispatcher-23", Username: "dispatcher-23", Role: auth.RoleDispatcher, SessionID: "dispatch-session"}
	if err := store.CreateUser(ctx, auth.User{ID: principal.UserID, Username: principal.Username, PasswordHash: "hash", Role: principal.Role, Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create dispatcher: %v", err)
	}
	region := fleet.Region{ID: "dispatch-region", Code: "DISP23", Name: "Dispatch Region", Timezone: "UTC", Status: fleet.RegionActive, MaxVehicles: 5, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	safetyUntil := now.Add(24 * time.Hour)
	firstVehicle := fleet.Vehicle{ID: "dispatch-vehicle-23a", RegionID: region.ID, VIN: "VIN-DISPATCH-2301", FleetNumber: "DISP-23-A", Status: fleet.VehicleAvailable, Capability: "passenger", BatteryPercent: 80, Latitude: 31.2, Longitude: 121.4, SafetyValidUntil: &safetyUntil, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateVehicle(ctx, firstVehicle); err != nil {
		t.Fatalf("create first vehicle: %v", err)
	}
	dispatch := NewDispatch(store, clock.NewManual(now), idgen.NewSequence(2300))
	createMission := func(key, reference string) mission.Mission {
		value, createErr := dispatch.CreateMission(ctx, principal, CreateMissionInput{RegionID: region.ID, ExternalReference: reference, IdempotencyKey: key, Kind: "passenger", Priority: mission.PriorityUrgent, PickupLatitude: 31.20, PickupLongitude: 121.40, DropoffLatitude: 31.25, DropoffLongitude: 121.45, EarliestStartAt: now, DeadlineAt: now.Add(2 * time.Hour), MinimumBattery: 30, RequiredCapability: "passenger"})
		if createErr != nil {
			t.Fatalf("create mission %s: %v", key, createErr)
		}
		return value
	}
	failedMission := createMission("dispatch-failed-23", "OPS-DISPATCH-23-A")
	if err := store.AppendAudit(ctx, audit.Event{ID: "aud_00002302", ActorID: principal.UserID, ActorRole: string(principal.Role), Action: "fixture.reserve", ObjectType: "mission", ObjectID: failedMission.ID, Result: audit.ResultSuccess, RequestID: "occupied-dispatch-audit", Details: []byte("{}"), CreatedAt: now}); err != nil {
		t.Fatalf("reserve generated audit ID: %v", err)
	}
	_, err = dispatch.Dispatch(request.WithID(ctx, "dispatch-conflict"), principal, failedMission.ID)
	if !errors.Is(err, common.ErrConflict) {
		t.Fatalf("dispatch audit collision error = %v, want conflict", err)
	}
	storedMission, err := store.MissionByID(ctx, failedMission.ID)
	if err != nil {
		t.Fatalf("load failed mission: %v", err)
	}
	storedVehicle, err := store.VehicleByID(ctx, firstVehicle.ID)
	if err != nil {
		t.Fatalf("load failed vehicle: %v", err)
	}
	if storedMission.Status != mission.StatusPending || storedMission.AssignedVehicleID != "" || storedMission.Version != 1 {
		t.Fatalf("failed dispatch retained mission assignment: %+v", storedMission)
	}
	if storedVehicle.Status != fleet.VehicleAvailable || storedVehicle.Version != 1 {
		t.Fatalf("failed dispatch changed vehicle ownership: %+v", storedVehicle)
	}
	if _, err := store.TripByMissionID(ctx, failedMission.ID); !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("failed dispatch trip lookup error = %v, want not found", err)
	}
	count, err := store.AuditCountForRequest(ctx, "dispatch-conflict")
	if err != nil || count != 0 {
		t.Fatalf("failed dispatch audit count=%d err=%v, want zero", count, err)
	}

	secondVehicle := firstVehicle
	secondVehicle.ID = "dispatch-vehicle-23b"
	secondVehicle.VIN = "VIN-DISPATCH-2302"
	secondVehicle.FleetNumber = "DISP-23-B"
	secondVehicle.BatteryPercent = 95
	if err := store.CreateVehicle(ctx, secondVehicle); err != nil {
		t.Fatalf("create legal vehicle: %v", err)
	}
	legalMission := createMission("dispatch-legal-23", "OPS-DISPATCH-23-B")
	created, err := dispatch.Dispatch(request.WithID(ctx, "dispatch-legal"), principal, legalMission.ID)
	if err != nil {
		t.Fatalf("legal dispatch failed: %v", err)
	}
	legalStoredMission, _ := store.MissionByID(ctx, legalMission.ID)
	legalStoredVehicle, _ := store.VehicleByID(ctx, secondVehicle.ID)
	legalAudits, _ := store.AuditCountForRequest(ctx, "dispatch-legal")
	if created.Status != trip.StatusScheduled || legalStoredMission.Status != mission.StatusAssigned || legalStoredVehicle.Status != fleet.VehicleReserved || legalAudits != 1 {
		t.Fatalf("legal dispatch did not commit all entities: trip=%+v mission=%+v vehicle=%+v audits=%d", created, legalStoredMission, legalStoredVehicle, legalAudits)
	}
}
