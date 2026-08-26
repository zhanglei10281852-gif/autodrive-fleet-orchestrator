package service

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
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

type synchronizedIncidentClaimRepository struct {
	*sqlite.Store
	mu       sync.Mutex
	saveMu   sync.Mutex
	waiting  int
	allReady chan struct{}
}

func (r *synchronizedIncidentClaimRepository) SaveIncidentClaim(ctx context.Context, claimed safety.Incident) error {
	r.mu.Lock()
	r.waiting++
	if r.waiting == 2 {
		close(r.allReady)
	}
	ready := r.allReady
	r.mu.Unlock()
	<-ready
	r.saveMu.Lock()
	defer r.saveMu.Unlock()
	return r.Store.SaveIncidentClaim(ctx, claimed)
}

func TestConcurrentIncidentClaimsAllowSingleLeaseOwner(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "incident-claims.db"))
	if err != nil {
		t.Fatalf("open SQLite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	region := fleet.Region{ID: "region-claims", Code: "CLAIMS", Name: "Incident Claim Region", Timezone: "UTC", Status: fleet.RegionActive, MaxVehicles: 5, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	safetyUntil := now.Add(24 * time.Hour)
	vehicle := fleet.Vehicle{ID: "vehicle-claims", RegionID: region.ID, VIN: "VIN-CLAIMS-0001", FleetNumber: "AV-CLAIM-01", Status: fleet.VehicleSuspended, Capability: "passenger", BatteryPercent: 55, Latitude: 31.2, Longitude: 121.4, SafetyValidUntil: &safetyUntil, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateVehicle(ctx, vehicle); err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	for _, user := range []auth.User{
		{ID: "safety-alpha", Username: "alpha", PasswordHash: "hash-alpha", Role: auth.RoleSafetyOperator, Active: true, CreatedAt: now, UpdatedAt: now},
		{ID: "safety-bravo", Username: "bravo", PasswordHash: "hash-bravo", Role: auth.RoleSafetyOperator, Active: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.CreateUser(ctx, user); err != nil {
			t.Fatalf("create safety operator %s: %v", user.ID, err)
		}
	}
	incident := safety.Incident{ID: "incident-contended", VehicleID: vehicle.ID, Severity: safety.SeverityCritical, Category: "remote_stop", Summary: "vehicle requires immediate remote stop", Status: safety.StatusOpen, Version: 1, OpenedAt: now, UpdatedAt: now}
	if err := store.CreateIncident(ctx, incident); err != nil {
		t.Fatalf("create contended incident: %v", err)
	}

	repository := &synchronizedIncidentClaimRepository{Store: store, allReady: make(chan struct{})}
	operations := NewOperations(repository, clock.NewManual(now), idgen.NewSequence(1600), 2*time.Minute)
	principals := []auth.Principal{
		{UserID: "safety-alpha", Username: "alpha", Role: auth.RoleSafetyOperator, SessionID: "session-alpha"},
		{UserID: "safety-bravo", Username: "bravo", Role: auth.RoleSafetyOperator, SessionID: "session-bravo"},
	}
	type claimResult struct {
		value safety.Incident
		err   error
	}
	results := make(chan claimResult, len(principals))
	var wg sync.WaitGroup
	for _, principal := range principals {
		principal := principal
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := operations.ClaimIncident(ctx, principal, incident.ID, incident.Version)
			results <- claimResult{value: value, err: err}
		}()
	}
	wg.Wait()
	close(results)

	var successes, conflicts int
	owners := map[string]bool{}
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			owners[result.value.OwnerID] = true
		case errors.Is(result.err, common.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected claim result: value=%+v err=%v", result.value, result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("same-version claims produced successes=%d conflicts=%d owners=%v, want one winner", successes, conflicts, owners)
	}
	stored, err := store.IncidentByID(ctx, incident.ID)
	if err != nil {
		t.Fatalf("load contended incident: %v", err)
	}
	if stored.Status != safety.StatusAcknowledged || stored.Version != 2 || !owners[stored.OwnerID] || stored.LeaseUntil == nil || !stored.LeaseUntil.After(now) {
		t.Fatalf("durable lease does not match the single winner: stored=%+v owners=%v", stored, owners)
	}

	uncontended := incident
	uncontended.ID = "incident-uncontended"
	if err := store.CreateIncident(ctx, uncontended); err != nil {
		t.Fatalf("create uncontended incident: %v", err)
	}
	claimed, err := operations.ClaimIncident(ctx, principals[0], uncontended.ID, uncontended.Version)
	if err != nil {
		t.Fatalf("uncontended claim failed: %v", err)
	}
	if claimed.OwnerID != principals[0].UserID || claimed.Status != safety.StatusAcknowledged || claimed.Version != 2 || claimed.LeaseUntil == nil {
		t.Fatalf("uncontended claim returned invalid lease: %+v", claimed)
	}
}
