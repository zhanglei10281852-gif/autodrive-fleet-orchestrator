package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/trip"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/repository"
)

func TestTripCompletionFailureKeepsLinkedStateAtomic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)
	store, err := Open(ctx, filepath.Join(t.TempDir(), "trip-completion.db"))
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	operator := auth.User{ID: "operator-atomicity", Username: "atomicity-dispatcher", PasswordHash: "hash",
		Role: auth.RoleDispatcher, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateUser(ctx, operator); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	region := fleet.Region{ID: "region-atomicity", Code: "ATOMIC", Name: "Atomicity Zone", Timezone: "Asia/Shanghai",
		Status: fleet.RegionActive, MaxVehicles: 5, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	safetyUntil := now.Add(24 * time.Hour)
	vehicleValue := fleet.Vehicle{ID: "vehicle-atomicity", RegionID: region.ID, VIN: "VIN-ATOMICITY-001",
		FleetNumber: "ATOMIC-001", Status: fleet.VehicleAvailable, Capability: "passenger", BatteryPercent: 82,
		Latitude: 31.2, Longitude: 121.4, SafetyValidUntil: &safetyUntil, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateVehicle(ctx, vehicleValue); err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	missionValue := mission.Mission{ID: "mission-atomicity", RegionID: region.ID, ExternalReference: "external-atomicity",
		IdempotencyKey: "idempotency-atomicity", Kind: "passenger", Priority: mission.PriorityRoutine,
		Status: mission.StatusPending, PickupLatitude: 31.2, PickupLongitude: 121.4, DropoffLatitude: 31.3,
		DropoffLongitude: 121.5, EarliestStartAt: now, DeadlineAt: now.Add(time.Hour), MinimumBattery: 30,
		RequiredCapability: "passenger", Version: 1, CreatedBy: operator.ID, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateMission(ctx, missionValue); err != nil {
		t.Fatalf("create mission: %v", err)
	}

	assignedMission, err := missionValue.Assign(vehicleValue.ID)
	if err != nil {
		t.Fatalf("assign mission state: %v", err)
	}
	reservedVehicle, err := vehicleValue.Transition(fleet.VehicleReserved)
	if err != nil {
		t.Fatalf("reserve vehicle state: %v", err)
	}
	scheduledTrip := trip.Trip{ID: "trip-atomicity", MissionID: missionValue.ID, VehicleID: vehicleValue.ID,
		Status: trip.StatusScheduled, ScheduledAt: now, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CommitDispatch(ctx, repository.DispatchCommit{
		Mission: assignedMission, Vehicle: reservedVehicle, Trip: scheduledTrip,
		Audit: audit.Event{ID: "audit-atomicity-dispatch", ActorID: operator.ID, ActorRole: string(operator.Role),
			Action: "mission.dispatch", ObjectType: "mission", ObjectID: missionValue.ID, Result: audit.ResultSuccess,
			RequestID: "request-atomicity-dispatch", Details: []byte(`{}`), CreatedAt: now},
		ExpectedMissionVersion: missionValue.Version, ExpectedVehicleVersion: vehicleValue.Version,
	}); err != nil {
		t.Fatalf("commit dispatch: %v", err)
	}

	startedAt := now.Add(time.Minute)
	runningTrip, err := scheduledTrip.Start(startedAt)
	if err != nil {
		t.Fatalf("start trip state: %v", err)
	}
	runningMission, err := assignedMission.Transition(mission.StatusInProgress)
	if err != nil {
		t.Fatalf("start mission state: %v", err)
	}
	runningVehicle, err := reservedVehicle.Transition(fleet.VehicleInTrip)
	if err != nil {
		t.Fatalf("start vehicle state: %v", err)
	}
	if err := store.CommitTripStart(ctx, repository.TripStartCommit{
		Trip: runningTrip, Mission: runningMission, Vehicle: runningVehicle,
		Audit: audit.Event{ID: "audit-atomicity-start", ActorID: operator.ID, ActorRole: string(operator.Role),
			Action: "trip.start", ObjectType: "trip", ObjectID: scheduledTrip.ID, Result: audit.ResultSuccess,
			RequestID: "request-atomicity-start", Details: []byte(`{}`), CreatedAt: startedAt},
		ExpectedTripVersion: scheduledTrip.Version, ExpectedMissionVersion: assignedMission.Version,
		ExpectedVehicleVersion: reservedVehicle.Version,
	}); err != nil {
		t.Fatalf("commit trip start: %v", err)
	}

	poisonEvent := job.Outbox{ID: "event-atomicity-duplicate", Topic: "trip.completed", AggregateType: "trip",
		AggregateID: scheduledTrip.ID, Payload: []byte(`{"source":"preexisting"}`), Status: job.StatusPending,
		MaxAttempts: 3, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	if err := store.Enqueue(ctx, poisonEvent); err != nil {
		t.Fatalf("seed duplicate outbox identity: %v", err)
	}

	completedAt := startedAt.Add(10 * time.Minute)
	completedTrip, err := runningTrip.Complete(completedAt, 4200)
	if err != nil {
		t.Fatalf("complete trip state: %v", err)
	}
	completedMission, err := runningMission.Transition(mission.StatusCompleted)
	if err != nil {
		t.Fatalf("complete mission state: %v", err)
	}
	releasedVehicle, err := runningVehicle.Transition(fleet.VehicleAvailable)
	if err != nil {
		t.Fatalf("release vehicle state: %v", err)
	}
	completion := repository.TripCompletionCommit{
		Trip: completedTrip, Mission: completedMission, Vehicle: releasedVehicle,
		Audit: audit.Event{ID: "audit-atomicity-complete", ActorID: operator.ID, ActorRole: string(operator.Role),
			Action: "trip.complete", ObjectType: "trip", ObjectID: scheduledTrip.ID, Result: audit.ResultSuccess,
			RequestID: "request-atomicity-complete", Details: []byte(`{"distance_meters":4200}`), CreatedAt: completedAt},
		Outbox: poisonEvent, ExpectedTripVersion: runningTrip.Version, ExpectedMissionVersion: runningMission.Version,
		ExpectedVehicleVersion: runningVehicle.Version,
	}
	if err := store.CommitTripCompletion(ctx, completion); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("duplicate completion event should fail with conflict, got %v", err)
	}

	tripAfterFailure, err := store.TripByID(ctx, runningTrip.ID)
	if err != nil {
		t.Fatalf("load trip after failure: %v", err)
	}
	missionAfterFailure, err := store.MissionByID(ctx, runningMission.ID)
	if err != nil {
		t.Fatalf("load mission after failure: %v", err)
	}
	vehicleAfterFailure, err := store.VehicleByID(ctx, runningVehicle.ID)
	if err != nil {
		t.Fatalf("load vehicle after failure: %v", err)
	}
	if tripAfterFailure.Status != trip.StatusRunning || tripAfterFailure.Version != runningTrip.Version {
		t.Fatalf("failed completion changed trip: %+v", tripAfterFailure)
	}
	if missionAfterFailure.Status != mission.StatusInProgress || missionAfterFailure.Version != runningMission.Version {
		t.Fatalf("failed completion changed mission: %+v", missionAfterFailure)
	}
	if vehicleAfterFailure.Status != fleet.VehicleInTrip || vehicleAfterFailure.Version != runningVehicle.Version {
		t.Fatalf("failed completion released vehicle: %+v", vehicleAfterFailure)
	}
	if count, err := store.AuditCountForRequest(ctx, "request-atomicity-complete"); err != nil || count != 0 {
		t.Fatalf("failed completion leaked audit count=%d err=%v", count, err)
	}

	completion.Outbox.ID = "event-atomicity-valid"
	completion.Outbox.Payload = []byte(`{"source":"valid-completion"}`)
	if err := store.CommitTripCompletion(ctx, completion); err != nil {
		t.Fatalf("valid completion after rollback: %v", err)
	}
	finalTrip, _ := store.TripByID(ctx, runningTrip.ID)
	finalMission, _ := store.MissionByID(ctx, runningMission.ID)
	finalVehicle, _ := store.VehicleByID(ctx, runningVehicle.ID)
	finalEvent, eventErr := store.OutboxByID(ctx, completion.Outbox.ID)
	if finalTrip.Status != trip.StatusCompleted || finalMission.Status != mission.StatusCompleted || finalVehicle.Status != fleet.VehicleAvailable {
		t.Fatalf("valid completion did not update linked entities: trip=%s mission=%s vehicle=%s",
			finalTrip.Status, finalMission.Status, finalVehicle.Status)
	}
	if eventErr != nil || finalEvent.Status != job.StatusPending {
		t.Fatalf("valid completion did not persist outbox: event=%+v err=%v", finalEvent, eventErr)
	}
	if count, err := store.AuditCountForRequest(ctx, "request-atomicity-complete"); err != nil || count != 1 {
		t.Fatalf("valid completion audit count=%d err=%v", count, err)
	}
}
