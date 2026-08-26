package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/charging"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

func TestChargingCompletionOutboxFailureKeepsResourcesActive(t *testing.T) {
	store := openTestStore(t)
	user, region, vehicle := seedCore(t, store)
	ctx := context.Background()
	station := charging.Station{
		ID: "station-completion", RegionID: region.ID, Code: "ST-COMPLETE", Name: "Completion Station",
		Active: true, CreatedAt: fixedNow, UpdatedAt: fixedNow,
	}
	if err := store.CreateStation(ctx, station); err != nil {
		t.Fatalf("create station: %v", err)
	}
	connector := charging.Connector{
		ID: "connector-completion", StationID: station.ID, Code: "C-01", PowerKW: 120,
		Active: true, Version: 1,
	}
	if err := store.CreateConnector(ctx, connector); err != nil {
		t.Fatalf("create connector: %v", err)
	}
	session := charging.Session{
		ID: "charge-completion", VehicleID: vehicle.ID, ConnectorID: connector.ID,
		Status: charging.StatusReserved, WindowStart: fixedNow.Add(-5 * time.Minute),
		WindowEnd: fixedNow.Add(time.Hour), InitialBattery: vehicle.BatteryPercent,
		IdempotencyKey: "charge-completion-key", Version: 1, CreatedBy: user.ID, CreatedAt: fixedNow,
	}
	reserveAudit := audit.Event{
		ID: "audit-charge-reserve", ActorID: user.ID, ActorRole: string(user.Role),
		Action: "charging.reserve", ObjectType: "charging_session", ObjectID: session.ID,
		Result: audit.ResultSuccess, RequestID: "req-charge-reserve", Details: []byte(`{}`), CreatedAt: fixedNow,
	}
	if err := store.CreateChargingSession(ctx, session, reserveAudit); err != nil {
		t.Fatalf("create charging session: %v", err)
	}
	started, err := session.Start(fixedNow)
	if err != nil {
		t.Fatalf("start domain session: %v", err)
	}
	startAudit := audit.Event{
		ID: "audit-charge-start", ActorID: user.ID, ActorRole: string(user.Role),
		Action: "charging.start", ObjectType: "charging_session", ObjectID: session.ID,
		Result: audit.ResultSuccess, RequestID: "req-charge-start", Details: []byte(`{}`), CreatedAt: fixedNow,
	}
	if err := store.StartCharging(ctx, started, connector, vehicle.Version, fixedNow, startAudit); err != nil {
		t.Fatalf("start charging: %v", err)
	}
	chargingVehicle, err := store.VehicleByID(ctx, vehicle.ID)
	if err != nil {
		t.Fatalf("load charging vehicle: %v", err)
	}
	completedAt := fixedNow.Add(20 * time.Minute)
	completed, err := started.Complete(completedAt, 96, 42000)
	if err != nil {
		t.Fatalf("complete domain session: %v", err)
	}
	conflictingOutbox := job.Outbox{
		ID: "evt-charge-completion", Topic: "seed.conflict", AggregateType: "seed", AggregateID: "seed",
		Payload: []byte(`{"seed":true}`), Status: job.StatusPending, MaxAttempts: 3,
		AvailableAt: fixedNow, CreatedAt: fixedNow, UpdatedAt: fixedNow,
	}
	if err := store.Enqueue(ctx, conflictingOutbox); err != nil {
		t.Fatalf("seed conflicting outbox: %v", err)
	}
	failedAudit := audit.Event{
		ID: "audit-charge-complete-failed", ActorID: user.ID, ActorRole: string(user.Role),
		Action: "charging.complete", ObjectType: "charging_session", ObjectID: session.ID,
		Result: audit.ResultSuccess, RequestID: "req-charge-complete-failed", Details: []byte(`{}`), CreatedAt: completedAt,
	}
	completionEvent := job.Outbox{
		ID: conflictingOutbox.ID, Topic: "charging.completed", AggregateType: "charging_session", AggregateID: session.ID,
		Payload: []byte(`{"session_id":"charge-completion"}`), Status: job.StatusPending, MaxAttempts: 5,
		AvailableAt: completedAt, CreatedAt: completedAt, UpdatedAt: completedAt,
	}
	if err := store.CompleteCharging(ctx, completed, chargingVehicle.Version, completedAt, failedAudit, completionEvent); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("completion outbox conflict error=%v", err)
	}

	loadedSession, err := store.ChargingSessionByID(ctx, session.ID)
	if err != nil {
		t.Fatalf("load session after failed completion: %v", err)
	}
	loadedConnector, err := store.ConnectorByID(ctx, connector.ID)
	if err != nil {
		t.Fatalf("load connector after failed completion: %v", err)
	}
	loadedVehicle, err := store.VehicleByID(ctx, vehicle.ID)
	if err != nil {
		t.Fatalf("load vehicle after failed completion: %v", err)
	}
	if loadedSession.Status != charging.StatusActive || loadedSession.Version != started.Version {
		t.Fatalf("failed completion changed session: %+v", loadedSession)
	}
	if loadedConnector.LeaseOwnerID != session.ID || loadedConnector.LeaseUntil == nil {
		t.Fatalf("failed completion released connector: %+v", loadedConnector)
	}
	if loadedVehicle.Status != fleet.VehicleCharging || loadedVehicle.BatteryPercent != vehicle.BatteryPercent {
		t.Fatalf("failed completion changed vehicle: %+v", loadedVehicle)
	}
	if count, err := store.AuditCountForRequest(ctx, failedAudit.RequestID); err != nil || count != 0 {
		t.Fatalf("failed completion audit count=%d err=%v", count, err)
	}

	validAudit := failedAudit
	validAudit.ID = "audit-charge-complete-valid"
	validAudit.RequestID = "req-charge-complete-valid"
	validEvent := completionEvent
	validEvent.ID = "evt-charge-completion-valid"
	if err := store.CompleteCharging(ctx, completed, loadedVehicle.Version, completedAt, validAudit, validEvent); err != nil {
		t.Fatalf("valid completion: %v", err)
	}
	loadedSession, _ = store.ChargingSessionByID(ctx, session.ID)
	loadedConnector, _ = store.ConnectorByID(ctx, connector.ID)
	loadedVehicle, _ = store.VehicleByID(ctx, vehicle.ID)
	if loadedSession.Status != charging.StatusCompleted || loadedConnector.LeaseOwnerID != "" || loadedConnector.LeaseUntil != nil {
		t.Fatalf("valid completion did not release session resources: session=%+v connector=%+v", loadedSession, loadedConnector)
	}
	if loadedVehicle.Status != fleet.VehicleAvailable || loadedVehicle.BatteryPercent != 96 {
		t.Fatalf("valid completion did not restore charged vehicle: %+v", loadedVehicle)
	}
	if _, err := store.OutboxByID(ctx, validEvent.ID); err != nil {
		t.Fatalf("valid completion event missing: %v", err)
	}
	if count, err := store.AuditCountForRequest(ctx, validAudit.RequestID); err != nil || count != 1 {
		t.Fatalf("valid completion audit count=%d err=%v", count, err)
	}
}
