package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/maintenance"
)

func TestMaintenanceCompletionRollsBackWhenAuditFails(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 8, 26, 18, 0, 0, 0, time.UTC)
	operator := auth.User{ID: "maintenance-operator", Username: "maintenance-operator", PasswordHash: "hash", Role: auth.RoleSafetyOperator, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateUser(ctx, operator); err != nil {
		t.Fatalf("create operator: %v", err)
	}
	region := fleet.Region{ID: "maintenance-region", Code: "MAINT", Name: "Maintenance Region", Timezone: "Asia/Shanghai", Status: fleet.RegionActive, MaxVehicles: 4, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	safetyUntil := now.Add(24 * time.Hour)
	vehicle := fleet.Vehicle{ID: "maintenance-vehicle", RegionID: region.ID, VIN: "VIN-MAINT-00001", FleetNumber: "AV-MAINT-01", Status: fleet.VehicleAvailable, Capability: "passenger", BatteryPercent: 72, Latitude: 31.2, Longitude: 121.4, SafetyValidUntil: &safetyUntil, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateVehicle(ctx, vehicle); err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	order := maintenance.WorkOrder{ID: "maintenance-order", VehicleID: vehicle.ID, Status: maintenance.StatusOpen, Reason: "scheduled braking inspection", Priority: "high", PreviousVehicleStatus: string(vehicle.Status), RequiredChecks: []string{"brakes", "steering"}, Version: 1, CreatedBy: operator.ID, CreatedAt: now}
	openAudit := audit.Event{ID: "maintenance-open-audit", ActorID: operator.ID, ActorRole: string(operator.Role), Action: "maintenance.open", ObjectType: "maintenance_order", ObjectID: order.ID, Result: audit.ResultSuccess, RequestID: "maintenance-open-request", Details: []byte(`{}`), CreatedAt: now}
	if err := store.OpenMaintenance(ctx, order, vehicle, openAudit); err != nil {
		t.Fatalf("open maintenance: %v", err)
	}
	started, err := order.Start("technician-a", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("start maintenance: %v", err)
	}
	if err := store.UpdateMaintenance(ctx, started, order.Version); err != nil {
		t.Fatalf("persist maintenance start: %v", err)
	}
	checked, err := started.RecordCheck("brakes")
	if err != nil {
		t.Fatalf("record brakes check: %v", err)
	}
	checked, err = checked.RecordCheck("steering")
	if err != nil {
		t.Fatalf("record steering check: %v", err)
	}
	if err := store.UpdateMaintenance(ctx, checked, started.Version); err != nil {
		t.Fatalf("persist maintenance checks: %v", err)
	}
	completed, err := checked.Complete("inspection completed without defects", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("complete maintenance domain state: %v", err)
	}
	conflictingAudit := audit.Event{ID: "maintenance-completion-audit", ActorID: operator.ID, ActorRole: string(operator.Role), Action: "maintenance.complete", ObjectType: "maintenance_order", ObjectID: order.ID, Result: audit.ResultSuccess, RequestID: "maintenance-completion-request", Details: []byte(`{}`), CreatedAt: now.Add(2 * time.Minute)}
	if err := store.AppendAudit(ctx, conflictingAudit); err != nil {
		t.Fatalf("seed conflicting audit: %v", err)
	}
	loadedVehicle, err := store.VehicleByID(ctx, vehicle.ID)
	if err != nil {
		t.Fatalf("load maintenance vehicle: %v", err)
	}
	if err := store.CompleteMaintenance(ctx, completed, loadedVehicle, fleet.VehicleAvailable, now.Add(2*time.Minute), conflictingAudit); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("completion with conflicting audit error=%v, want conflict", err)
	}
	afterOrder, err := store.MaintenanceByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("load order after failed completion: %v", err)
	}
	afterVehicle, err := store.VehicleByID(ctx, vehicle.ID)
	if err != nil {
		t.Fatalf("load vehicle after failed completion: %v", err)
	}
	if afterOrder.Status != maintenance.StatusInProgress || afterOrder.Version != checked.Version {
		t.Fatalf("failed completion persisted order state: status=%s version=%d", afterOrder.Status, afterOrder.Version)
	}
	if afterVehicle.Status != fleet.VehicleMaintenance || afterVehicle.Version != loadedVehicle.Version {
		t.Fatalf("failed completion released vehicle: status=%s version=%d", afterVehicle.Status, afterVehicle.Version)
	}

	validAudit := conflictingAudit
	validAudit.ID = "maintenance-completion-audit-success"
	validAudit.RequestID = "maintenance-completion-request-success"
	if err := store.CompleteMaintenance(ctx, completed, afterVehicle, fleet.VehicleAvailable, now.Add(2*time.Minute), validAudit); err != nil {
		t.Fatalf("valid completion: %v", err)
	}
	finalOrder, err := store.MaintenanceByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("load completed order: %v", err)
	}
	finalVehicle, err := store.VehicleByID(ctx, vehicle.ID)
	if err != nil {
		t.Fatalf("load released vehicle: %v", err)
	}
	if finalOrder.Status != maintenance.StatusCompleted || finalVehicle.Status != fleet.VehicleAvailable {
		t.Fatalf("valid completion not atomic: order=%s vehicle=%s", finalOrder.Status, finalVehicle.Status)
	}
	if _, err := store.AuditEventByID(ctx, validAudit.ID); err != nil {
		t.Fatalf("valid completion audit missing: %v", err)
	}
}
