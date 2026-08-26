package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/safety"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

type vehicleSnapshotInterleavingRepository struct {
	*sqlite.Store
	vehicleID    string
	snapshotRead chan struct{}
	resumeCommit chan struct{}
	once         sync.Once
}

func (r *vehicleSnapshotInterleavingRepository) VehicleByID(ctx context.Context, id string) (fleet.Vehicle, error) {
	vehicle, err := r.Store.VehicleByID(ctx, id)
	if err != nil || id != r.vehicleID {
		return vehicle, err
	}
	r.once.Do(func() {
		close(r.snapshotRead)
		<-r.resumeCommit
	})
	return vehicle, nil
}

func TestIncidentResolutionRejectsVehicleStateChangeAfterRead(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	principal := auth.Principal{UserID: "safety-operator", Username: "safety", Role: auth.RoleSafetyOperator, SessionID: "session-1"}

	seed := func(t *testing.T) (*sqlite.Store, fleet.Vehicle, safety.Incident) {
		t.Helper()
		store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "incident.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		user := auth.User{ID: principal.UserID, Username: principal.Username, PasswordHash: "stored-password-hash", Role: principal.Role, Active: true, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateUser(ctx, user); err != nil {
			t.Fatalf("create operator: %v", err)
		}
		region := fleet.Region{ID: "region-1", Code: "SAFE", Name: "Safety Region", Timezone: "Asia/Shanghai", Status: fleet.RegionActive, MaxVehicles: 10, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateRegion(ctx, region); err != nil {
			t.Fatalf("create region: %v", err)
		}
		safetyUntil := now.Add(24 * time.Hour)
		vehicle := fleet.Vehicle{ID: "vehicle-1", RegionID: region.ID, VIN: "VIN-SAFETY-0001", FleetNumber: "SAFE-001", Status: fleet.VehicleSuspended, Capability: "passenger", BatteryPercent: 70, SafetyValidUntil: &safetyUntil, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateVehicle(ctx, vehicle); err != nil {
			t.Fatalf("create vehicle: %v", err)
		}
		leaseUntil := now.Add(10 * time.Minute)
		incident := safety.Incident{ID: "incident-1", VehicleID: vehicle.ID, Severity: safety.SeverityHigh, Category: "braking", Summary: "braking system intervention", Status: safety.StatusMitigating, OwnerID: principal.UserID, LeaseUntil: &leaseUntil, Version: 3, OpenedAt: now.Add(-time.Hour), UpdatedAt: now}
		if err := store.CreateIncident(ctx, incident); err != nil {
			t.Fatalf("create incident: %v", err)
		}
		return store, vehicle, incident
	}

	t.Run("durable vehicle change rejects stale resolution", func(t *testing.T) {
		store, vehicle, incident := seed(t)
		repository := &vehicleSnapshotInterleavingRepository{Store: store, vehicleID: vehicle.ID, snapshotRead: make(chan struct{}), resumeCommit: make(chan struct{})}
		operations := service.NewOperations(repository, clock.NewManual(now), idgen.NewSequence(1), 10*time.Minute)
		result := make(chan error, 1)
		go func() {
			_, err := operations.ResolveIncident(ctx, principal, incident.ID, "vehicle inspected and secured", incident.Version)
			result <- err
		}()

		<-repository.snapshotRead
		available := vehicle
		available.Status = fleet.VehicleAvailable
		available.Version++
		available.UpdatedAt = now.Add(time.Second)
		if err := store.UpdateVehicle(ctx, available, vehicle.Version); err != nil {
			t.Fatalf("change durable vehicle state: %v", err)
		}
		close(repository.resumeCommit)

		if err := <-result; !errors.Is(err, common.ErrConflict) {
			t.Fatalf("resolution error = %v, want conflict after vehicle state changed", err)
		}
		persisted, err := store.IncidentByID(ctx, incident.ID)
		if err != nil {
			t.Fatalf("reload incident: %v", err)
		}
		if persisted.Status != safety.StatusMitigating || persisted.Version != incident.Version {
			t.Fatalf("stale resolution changed incident: %+v", persisted)
		}
	})

	t.Run("unchanged safe vehicle resolves normally", func(t *testing.T) {
		store, _, incident := seed(t)
		operations := service.NewOperations(store, clock.NewManual(now), idgen.NewSequence(20), 10*time.Minute)
		resolved, err := operations.ResolveIncident(ctx, principal, incident.ID, "vehicle inspected and secured", incident.Version)
		if err != nil {
			t.Fatalf("resolve incident: %v", err)
		}
		if resolved.Status != safety.StatusResolved || resolved.Version != incident.Version+1 {
			t.Fatalf("unexpected resolved incident: %+v", resolved)
		}
		if count, err := store.AuditCountForRequest(ctx, "internal"); err != nil || count != 1 {
			t.Fatalf("resolution audit count = %d, err = %v, want 1", count, err)
		}
	})
}
