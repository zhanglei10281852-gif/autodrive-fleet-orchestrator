package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/trip"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/repository"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

type vehicleConflictAfterTripStartRepository struct {
	*sqlite.Store
	injectVehicleConflict bool
}

func (r *vehicleConflictAfterTripStartRepository) CommitTripAndMissionStart(ctx context.Context, commit repository.TripStartCommit) error {
	if err := r.Store.CommitTripAndMissionStart(ctx, commit); err != nil {
		return err
	}
	if !r.injectVehicleConflict {
		return nil
	}
	r.injectVehicleConflict = false
	current, err := r.Store.VehicleByID(ctx, commit.Vehicle.ID)
	if err != nil {
		return err
	}
	suspended, err := current.Transition(fleet.VehicleSuspended)
	if err != nil {
		return err
	}
	suspended.UpdatedAt = commit.Vehicle.UpdatedAt
	return r.Store.UpdateVehicle(ctx, suspended, current.Version)
}

func TestTripStartConflictRollsBackAllAssignedState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "trip-start.db"))
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	principal := auth.Principal{UserID: "dispatcher-start", Username: "dispatcher", Role: auth.RoleDispatcher, SessionID: "session-start"}
	user := auth.User{ID: principal.UserID, Username: principal.Username, PasswordHash: "hash-dispatcher", Role: principal.Role, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("create dispatcher: %v", err)
	}
	region := fleet.Region{ID: "region-start", Code: "START", Name: "Trip Start Region", Timezone: "UTC", Status: fleet.RegionActive, MaxVehicles: 5, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	safetyUntil := now.Add(24 * time.Hour)
	firstVehicle := fleet.Vehicle{ID: "vehicle-start-conflict", RegionID: region.ID, VIN: "VIN-START-0001", FleetNumber: "AV-START-01", Status: fleet.VehicleAvailable, Capability: "passenger", BatteryPercent: 80, Latitude: 31.2, Longitude: 121.4, SafetyValidUntil: &safetyUntil, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateVehicle(ctx, firstVehicle); err != nil {
		t.Fatalf("create first vehicle: %v", err)
	}

	repositoryWithConflict := &vehicleConflictAfterTripStartRepository{Store: store, injectVehicleConflict: true}
	dispatch := NewDispatch(repositoryWithConflict, clock.NewManual(now), idgen.NewSequence(1700))
	createMission := func(key, reference string) mission.Mission {
		value, createErr := dispatch.CreateMission(ctx, principal, CreateMissionInput{
			RegionID: region.ID, ExternalReference: reference, IdempotencyKey: key, Kind: "passenger",
			Priority: mission.PriorityUrgent, PickupLatitude: 31.20, PickupLongitude: 121.40,
			DropoffLatitude: 31.25, DropoffLongitude: 121.45, EarliestStartAt: now,
			DeadlineAt: now.Add(2 * time.Hour), MinimumBattery: 30, RequiredCapability: "passenger",
		})
		if createErr != nil {
			t.Fatalf("create mission %s: %v", key, createErr)
		}
		return value
	}

	contendedMission := createMission("trip-start-conflict", "OPS-START-001")
	contendedTrip, err := dispatch.Dispatch(ctx, principal, contendedMission.ID)
	if err != nil {
		t.Fatalf("dispatch contended trip: %v", err)
	}
	_, err = dispatch.StartTrip(ctx, principal, contendedTrip.ID)
	if !errors.Is(err, common.ErrConflict) {
		t.Fatalf("start after competing vehicle transition error = %v, want conflict", err)
	}
	storedTrip, err := store.TripByID(ctx, contendedTrip.ID)
	if err != nil {
		t.Fatalf("load contended trip: %v", err)
	}
	storedMission, err := store.MissionByID(ctx, contendedMission.ID)
	if err != nil {
		t.Fatalf("load contended mission: %v", err)
	}
	storedVehicle, err := store.VehicleByID(ctx, firstVehicle.ID)
	if err != nil {
		t.Fatalf("load contended vehicle: %v", err)
	}
	if storedTrip.Status != trip.StatusScheduled || storedTrip.StartedAt != nil || storedMission.Status != mission.StatusAssigned {
		t.Fatalf("failed start leaked partial state: trip=%+v mission=%+v", storedTrip, storedMission)
	}
	if storedVehicle.Status != fleet.VehicleSuspended {
		t.Fatalf("competing vehicle transition was not preserved: %+v", storedVehicle)
	}

	secondVehicle := firstVehicle
	secondVehicle.ID = "vehicle-start-legal"
	secondVehicle.VIN = "VIN-START-0002"
	secondVehicle.FleetNumber = "AV-START-02"
	secondVehicle.Version = 1
	secondVehicle.Status = fleet.VehicleAvailable
	if err := store.CreateVehicle(ctx, secondVehicle); err != nil {
		t.Fatalf("create legal vehicle: %v", err)
	}
	legalMission := createMission("trip-start-legal", "OPS-START-002")
	legalTrip, err := dispatch.Dispatch(ctx, principal, legalMission.ID)
	if err != nil {
		t.Fatalf("dispatch legal trip: %v", err)
	}
	started, err := dispatch.StartTrip(ctx, principal, legalTrip.ID)
	if err != nil {
		t.Fatalf("uncontended start failed: %v", err)
	}
	legalStoredMission, err := store.MissionByID(ctx, legalMission.ID)
	if err != nil {
		t.Fatalf("load legal mission: %v", err)
	}
	legalStoredVehicle, err := store.VehicleByID(ctx, secondVehicle.ID)
	if err != nil {
		t.Fatalf("load legal vehicle: %v", err)
	}
	if started.Status != trip.StatusRunning || started.StartedAt == nil || legalStoredMission.Status != mission.StatusInProgress || legalStoredVehicle.Status != fleet.VehicleInTrip {
		t.Fatalf("uncontended start did not advance all entities: trip=%+v mission=%+v vehicle=%+v", started, legalStoredMission, legalStoredVehicle)
	}
}
