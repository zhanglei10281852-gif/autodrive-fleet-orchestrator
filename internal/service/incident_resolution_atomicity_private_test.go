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
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/safety"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestIncidentResolutionAuditFailurePreservesMitigationLease(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "incident-resolution.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	principal := auth.Principal{UserID: "safety-24", Username: "safety-24", Role: auth.RoleSafetyOperator, SessionID: "session-24"}
	if err := store.CreateUser(ctx, auth.User{ID: principal.UserID, Username: principal.Username, PasswordHash: "hash", Role: principal.Role, Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	region := fleet.Region{ID: "region-24", Code: "SAFE24", Name: "Safety Region", Timezone: "UTC", Status: fleet.RegionActive, MaxVehicles: 5, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	vehicle := fleet.Vehicle{ID: "vehicle-24", RegionID: region.ID, VIN: "VIN-SAFETY-2400", FleetNumber: "SAFE-24", Status: fleet.VehicleSuspended, Capability: "passenger", BatteryPercent: 60, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateVehicle(ctx, vehicle); err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	lease := now.Add(10 * time.Minute)
	seedIncident := func(id string) safety.Incident {
		value := safety.Incident{ID: id, VehicleID: vehicle.ID, Severity: safety.SeverityHigh, Category: "braking", Summary: "braking intervention", Status: safety.StatusMitigating, OwnerID: principal.UserID, LeaseUntil: &lease, Version: 3, OpenedAt: now.Add(-time.Hour), UpdatedAt: now}
		if err := store.CreateIncident(ctx, value); err != nil {
			t.Fatalf("create incident %s: %v", id, err)
		}
		return value
	}
	failed := seedIncident("incident-24-failed")
	if err := store.AppendAudit(ctx, audit.Event{ID: "aud_00002400", ActorID: principal.UserID, ActorRole: string(principal.Role), Action: "fixture.reserve", ObjectType: "incident", ObjectID: failed.ID, Result: audit.ResultSuccess, RequestID: "occupied-incident-audit", Details: []byte("{}"), CreatedAt: now}); err != nil {
		t.Fatalf("reserve audit ID: %v", err)
	}
	operations := NewOperations(store, clock.NewManual(now), idgen.NewSequence(2400), 10*time.Minute)
	_, err = operations.ResolveIncident(request.WithID(ctx, "incident-resolve-conflict"), principal, failed.ID, "vehicle secured", failed.Version)
	if !errors.Is(err, common.ErrConflict) {
		t.Fatalf("resolution audit collision error=%v, want conflict", err)
	}
	stored, err := store.IncidentByID(ctx, failed.ID)
	if err != nil {
		t.Fatalf("reload failed incident: %v", err)
	}
	if stored.Status != safety.StatusMitigating || stored.Version != failed.Version || stored.LeaseUntil == nil || stored.OwnerID != principal.UserID {
		t.Fatalf("failed resolution released mitigation lease: %+v", stored)
	}
	count, err := store.AuditCountForRequest(ctx, "incident-resolve-conflict")
	if err != nil || count != 0 {
		t.Fatalf("failed resolution audit count=%d err=%v, want zero", count, err)
	}
	legal := seedIncident("incident-24-legal")
	resolved, err := operations.ResolveIncident(request.WithID(ctx, "incident-resolve-legal"), principal, legal.ID, "inspection complete", legal.Version)
	if err != nil {
		t.Fatalf("legal resolution failed: %v", err)
	}
	legalStored, _ := store.IncidentByID(ctx, legal.ID)
	legalAudits, _ := store.AuditCountForRequest(ctx, "incident-resolve-legal")
	if resolved.Status != safety.StatusResolved || legalStored.LeaseUntil != nil || legalAudits != 1 {
		t.Fatalf("legal resolution did not commit state and audit: resolved=%+v stored=%+v audits=%d", resolved, legalStored, legalAudits)
	}
}
