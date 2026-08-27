package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/telemetry"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/trip"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/repository"
)

var fixedNow = time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "autodrive.db")
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func seedCore(t *testing.T, store *Store) (auth.User, fleet.Region, fleet.Vehicle) {
	t.Helper()
	ctx := context.Background()
	user := auth.User{ID: "u1", Username: "dispatcher", PasswordHash: "hash", Role: auth.RoleDispatcher, Active: true, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	region := fleet.Region{ID: "r1", Code: "PUDONG", Name: "Pudong", Timezone: "Asia/Shanghai", Status: fleet.RegionActive, MaxVehicles: 20, Version: 1, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	safetyUntil := fixedNow.Add(24 * time.Hour)
	vehicle := fleet.Vehicle{ID: "v1", RegionID: region.ID, VIN: "VIN00000001", FleetNumber: "AV-001", Status: fleet.VehicleAvailable,
		Capability: "passenger", BatteryPercent: 80, Latitude: 31.2, Longitude: 121.4, SafetyValidUntil: &safetyUntil,
		Version: 1, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if err := store.CreateVehicle(ctx, vehicle); err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	return user, region, vehicle
}

func TestMigrationsCreateExpectedSchema(t *testing.T) {
	store := openTestStore(t)
	rows, err := store.db.QueryContext(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names[name] = true
	}
	expected := []string{"schema_migrations", "users", "sessions", "regions", "vehicles", "missions", "trips",
		"audit_events", "outbox_events", "telemetry_samples", "safety_incidents", "charging_stations",
		"charging_connectors", "charging_sessions", "maintenance_orders", "service_readiness", "idempotency_records"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected table %s", name)
		}
	}
	var migrations int
	if err := store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if migrations != 3 {
		t.Fatalf("migration count=%d want 3", migrations)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("repeated migrate failed: %v", err)
	}
	var repeated int
	_ = store.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&repeated)
	if repeated != migrations {
		t.Fatalf("repeated migration changed ledger: %d -> %d", migrations, repeated)
	}
}

func TestForeignKeysAndUniqueConstraints(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	safetyUntil := fixedNow.Add(time.Hour)
	vehicle := fleet.Vehicle{ID: "missing-region-vehicle", RegionID: "missing", VIN: "VIN-MISSING-1", FleetNumber: "AV-X",
		Status: fleet.VehicleAvailable, Capability: "passenger", BatteryPercent: 90, SafetyValidUntil: &safetyUntil,
		Version: 1, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if err := store.CreateVehicle(ctx, vehicle); err == nil {
		t.Fatal("foreign key violation unexpectedly succeeded")
	}
	_, _, seeded := seedCore(t, store)
	duplicate := seeded
	duplicate.ID = "v2"
	duplicate.FleetNumber = "AV-002"
	if err := store.CreateVehicle(ctx, duplicate); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("duplicate VIN should conflict: %v", err)
	}
	duplicate = seeded
	duplicate.ID = "v3"
	duplicate.VIN = "VIN00000003"
	if err := store.CreateVehicle(ctx, duplicate); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("duplicate fleet number should conflict: %v", err)
	}
}

func TestSessionLifecyclePersistsAndRevokes(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	user, _, _ := seedCore(t, store)
	session := auth.Session{ID: "s1", UserID: user.ID, TokenHash: "token-hash", ExpiresAt: fixedNow.Add(time.Hour), CreatedAt: fixedNow, LastSeen: fixedNow}
	if err := store.CreateSessionWithAudit(ctx, session, "a-login", "req-login"); err != nil {
		t.Fatalf("create session with audit: %v", err)
	}
	loaded, loadedUser, err := store.SessionByTokenHash(ctx, session.TokenHash)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	if loaded.ID != session.ID || loadedUser.ID != user.ID || loaded.RevokedAt != nil {
		t.Fatalf("unexpected session load: %+v %+v", loaded, loadedUser)
	}
	if count, err := store.AuditCountForRequest(ctx, "req-login"); err != nil || count != 1 {
		t.Fatalf("login audit count=%d err=%v", count, err)
	}
	if err := store.RevokeSession(ctx, session.ID, user.ID, "a-logout", "req-logout", fixedNow.Add(time.Minute)); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	revoked, _, err := store.SessionByTokenHash(ctx, session.TokenHash)
	if err != nil {
		t.Fatalf("load revoked session: %v", err)
	}
	if revoked.RevokedAt == nil {
		t.Fatal("revoked session has no timestamp")
	}
	if count, err := store.AuditCountForRequest(ctx, "req-logout"); err != nil || count != 1 {
		t.Fatalf("logout audit count=%d err=%v", count, err)
	}
	deleted, err := store.DeleteExpiredSessions(ctx, fixedNow.Add(2*time.Hour), 100)
	if err != nil || deleted != 1 {
		t.Fatalf("delete expired count=%d err=%v", deleted, err)
	}
	if _, _, err := store.SessionByTokenHash(ctx, session.TokenHash); !errors.Is(err, common.ErrNotFound) {
		t.Fatalf("deleted session still available: %v", err)
	}
}

func TestRestartRecoversPersistedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")
	ctx := context.Background()
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_, region, vehicle := seedCore(t, first)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer second.Close()
	loadedRegion, err := second.RegionByID(ctx, region.ID)
	if err != nil {
		t.Fatalf("load region after restart: %v", err)
	}
	loadedVehicle, err := second.VehicleByID(ctx, vehicle.ID)
	if err != nil {
		t.Fatalf("load vehicle after restart: %v", err)
	}
	if loadedRegion.Code != region.Code || loadedVehicle.VIN != vehicle.VIN || loadedVehicle.Status != fleet.VehicleAvailable {
		t.Fatalf("state changed after restart: %+v %+v", loadedRegion, loadedVehicle)
	}
}

func TestOptimisticVehicleUpdateAllowsOneWinner(t *testing.T) {
	store := openTestStore(t)
	_, _, vehicle := seedCore(t, store)
	ctx := context.Background()
	barrier := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for index := 0; index < 2; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			candidate := vehicle
			candidate.BatteryPercent = 70 + index
			candidate.Version = vehicle.Version + 1
			candidate.UpdatedAt = fixedNow.Add(time.Duration(index+1) * time.Minute)
			<-barrier
			results <- store.UpdateVehicle(ctx, candidate, vehicle.Version)
		}(index)
	}
	close(barrier)
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, common.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected update error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	loaded, err := store.VehicleByID(ctx, vehicle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != vehicle.Version+1 || (loaded.BatteryPercent != 70 && loaded.BatteryPercent != 71) {
		t.Fatalf("unexpected winning vehicle: %+v", loaded)
	}
}

func TestDispatchCommitIsAtomicAndRejectsSecondOwner(t *testing.T) {
	store := openTestStore(t)
	user, region, vehicle := seedCore(t, store)
	ctx := context.Background()
	missionValue := mission.Mission{ID: "m1", RegionID: region.ID, ExternalReference: "ext", IdempotencyKey: "key",
		Kind: "passenger", Priority: mission.PriorityRoutine, Status: mission.StatusPending,
		PickupLatitude: 31.2, PickupLongitude: 121.4, DropoffLatitude: 31.3, DropoffLongitude: 121.5,
		EarliestStartAt: fixedNow, DeadlineAt: fixedNow.Add(time.Hour), MinimumBattery: 20,
		RequiredCapability: "passenger", Version: 1, CreatedBy: user.ID, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if err := store.CreateMission(ctx, missionValue); err != nil {
		t.Fatal(err)
	}
	assigned, _ := missionValue.Assign(vehicle.ID)
	assigned.UpdatedAt = fixedNow
	reserved, _ := vehicle.Transition(fleet.VehicleReserved)
	reserved.UpdatedAt = fixedNow
	tripValue := trip.Trip{ID: "t1", MissionID: missionValue.ID, VehicleID: vehicle.ID, Status: trip.StatusScheduled,
		ScheduledAt: fixedNow, Version: 1, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	event := audit.Event{ID: "a1", ActorID: user.ID, ActorRole: string(user.Role), Action: "mission.dispatch",
		ObjectType: "mission", ObjectID: missionValue.ID, Result: audit.ResultSuccess, RequestID: "req", Details: []byte(`{}`), CreatedAt: fixedNow}
	commit := repository.DispatchCommit{Mission: assigned, Vehicle: reserved, Trip: tripValue, Audit: event,
		ExpectedMissionVersion: missionValue.Version, ExpectedVehicleVersion: vehicle.Version}
	if err := store.CommitDispatch(ctx, commit); err != nil {
		t.Fatalf("commit dispatch: %v", err)
	}
	loadedMission, _ := store.MissionByID(ctx, missionValue.ID)
	loadedVehicle, _ := store.VehicleByID(ctx, vehicle.ID)
	loadedTrip, _ := store.TripByID(ctx, tripValue.ID)
	if loadedMission.Status != mission.StatusAssigned || loadedVehicle.Status != fleet.VehicleReserved || loadedTrip.Status != trip.StatusScheduled {
		t.Fatalf("inconsistent dispatch: %+v %+v %+v", loadedMission, loadedVehicle, loadedTrip)
	}
	second := tripValue
	second.ID = "t2"
	second.MissionID = "missing-mission"
	commit.Trip = second
	commit.Audit.ID = "a2"
	if err := store.CommitDispatch(ctx, commit); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("second dispatch should conflict: %v", err)
	}
	var tripCount int
	_ = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM trips`).Scan(&tripCount)
	if tripCount != 1 {
		t.Fatalf("failed dispatch leaked trip: %d", tripCount)
	}
}

func TestCreateMissionIdempotencyKeyConvergesUnderConcurrentRetry(t *testing.T) {
	store := openTestStore(t)
	user, region, _ := seedCore(t, store)
	ctx := context.Background()

	first := mission.Mission{ID: "m-concurrent-1", RegionID: region.ID, ExternalReference: "ext", IdempotencyKey: "same-key",
		Kind: "passenger", Priority: mission.PriorityRoutine, Status: mission.StatusPending,
		PickupLatitude: 31.2, PickupLongitude: 121.4, DropoffLatitude: 31.3, DropoffLongitude: 121.5,
		EarliestStartAt: fixedNow, DeadlineAt: fixedNow.Add(time.Hour), MinimumBattery: 20,
		RequiredCapability: "passenger", Version: 1, CreatedBy: user.ID, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if err := store.CreateMission(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// A concurrent retry reusing the same idempotency key with identical
	// content must fail the UNIQUE constraint, and that conflict must unwrap to
	// ErrConflict so DispatchService.CreateMission can reconcile it.
	second := first
	second.ID = "m-concurrent-2"
	err := store.CreateMission(ctx, second)
	if err == nil {
		t.Fatal("duplicate idempotency key unexpectedly succeeded")
	}
	if !errors.Is(err, common.ErrConflict) {
		t.Fatalf("concurrent retry must surface a conflict that reconciles, got %v", err)
	}

	loaded, err := store.MissionByIdempotency(ctx, user.ID, "same-key")
	if err != nil {
		t.Fatalf("re-read by idempotency key: %v", err)
	}
	if loaded.ID != "m-concurrent-1" {
		t.Fatalf("expected the committed mission to win, got %s", loaded.ID)
	}

	// Reusing the key with divergent content stays rejected — reconciliation
	// must surface the persisted winner, not the losing request.
	divergent := first
	divergent.ID = "m-concurrent-3"
	divergent.Kind = "cargo"
	if err := store.CreateMission(ctx, divergent); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("divergent content reusing the key must still conflict: %v", err)
	}
}

func TestTelemetryIsIdempotentAndDoesNotRegressSnapshot(t *testing.T) {
	store := openTestStore(t)
	_, _, vehicle := seedCore(t, store)
	ctx := context.Background()
	newer := telemetry.Sample{EventID: "e-new", VehicleID: vehicle.ID, ObservedAt: fixedNow.Add(time.Minute), Latitude: 31.5,
		Longitude: 121.6, SpeedKPH: 20, BatteryPercent: 70, OdometerMeters: 1000, Severity: telemetry.SeverityInfo,
		PayloadHash: "hash-new", ReceivedAt: fixedNow.Add(time.Minute)}
	duplicate, err := store.CommitTelemetry(ctx, repository.TelemetryCommit{Sample: newer})
	if err != nil || duplicate {
		t.Fatalf("commit telemetry duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = store.CommitTelemetry(ctx, repository.TelemetryCommit{Sample: newer})
	if err != nil || !duplicate {
		t.Fatalf("repeat telemetry duplicate=%v err=%v", duplicate, err)
	}
	conflicting := newer
	conflicting.PayloadHash = "different"
	if _, err := store.CommitTelemetry(ctx, repository.TelemetryCommit{Sample: conflicting}); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("event reuse should conflict: %v", err)
	}
	older := newer
	older.EventID = "e-old"
	older.ObservedAt = fixedNow
	older.BatteryPercent = 10
	older.Latitude = 30
	older.PayloadHash = "hash-old"
	if _, err := store.CommitTelemetry(ctx, repository.TelemetryCommit{Sample: older}); err != nil {
		t.Fatalf("commit older telemetry: %v", err)
	}
	loaded, _ := store.VehicleByID(ctx, vehicle.ID)
	if loaded.BatteryPercent != newer.BatteryPercent || loaded.Latitude != newer.Latitude || loaded.LastTelemetryAt == nil || !loaded.LastTelemetryAt.Equal(newer.ObservedAt) {
		t.Fatalf("older telemetry regressed snapshot: %+v", loaded)
	}
}

func TestOutboxLeaseRecoveryAndCompletion(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	event := job.Outbox{ID: "e1", Topic: "trip.completed", AggregateType: "trip", AggregateID: "t1",
		Payload: []byte(`{"trip":"t1"}`), Status: job.StatusPending, MaxAttempts: 3,
		AvailableAt: fixedNow, CreatedAt: fixedNow, UpdatedAt: fixedNow}
	if err := store.Enqueue(ctx, event); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimOutbox(ctx, "worker-a", fixedNow, time.Minute, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim len=%d err=%v", len(first), err)
	}
	active, err := store.ClaimOutbox(ctx, "worker-b", fixedNow.Add(30*time.Second), time.Minute, 10)
	if err != nil || len(active) != 0 {
		t.Fatalf("active lease stolen len=%d err=%v", len(active), err)
	}
	recovered, err := store.ClaimOutbox(ctx, "worker-b", fixedNow.Add(2*time.Minute), time.Minute, 10)
	if err != nil || len(recovered) != 1 || recovered[0].LeaseOwner != "worker-b" {
		t.Fatalf("expired lease not recovered: %+v err=%v", recovered, err)
	}
	if err := store.CompleteOutbox(ctx, event.ID, "worker-a", fixedNow); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("old owner completed lease: %v", err)
	}
	if err := store.CompleteOutbox(ctx, event.ID, "worker-b", fixedNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("current owner completion failed: %v", err)
	}
	loaded, err := store.OutboxByID(ctx, event.ID)
	if err != nil || loaded.Status != job.StatusCompleted {
		t.Fatalf("outbox not completed: %+v err=%v", loaded, err)
	}
}

func TestContextCancellationRollsBackTransaction(t *testing.T) {
	store := openTestStore(t)
	_, region, _ := seedCore(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	err := store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE regions SET name = 'changed' WHERE id = ?`, region.ID); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	loaded, loadErr := store.RegionByID(context.Background(), region.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.Name != region.Name {
		t.Fatalf("cancelled transaction committed: %s", loaded.Name)
	}
}

func TestVehicleListUsesSameFilterForCountAndRows(t *testing.T) {
	store := openTestStore(t)
	_, region, vehicle := seedCore(t, store)
	ctx := context.Background()
	for index := 2; index <= 6; index++ {
		candidate := vehicle
		candidate.ID = fmt.Sprintf("v%d", index)
		candidate.VIN = fmt.Sprintf("VIN0000000%d", index)
		candidate.FleetNumber = fmt.Sprintf("AV-%03d", index)
		candidate.BatteryPercent = index * 10
		if index%2 == 0 {
			candidate.Status = fleet.VehicleOffline
		}
		if err := store.CreateVehicle(ctx, candidate); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListVehicles(ctx, fleet.Filter{RegionID: region.ID, Status: fleet.VehicleAvailable,
		MinBattery: 30, Page: common.PageRequest{Limit: 2, Offset: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 3 || len(page.Items) != 2 {
		t.Fatalf("unexpected first page total=%d items=%d", page.Total, len(page.Items))
	}
	second, err := store.ListVehicles(ctx, fleet.Filter{RegionID: region.ID, Status: fleet.VehicleAvailable,
		MinBattery: 30, Page: common.PageRequest{Limit: 2, Offset: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != page.Total || len(second.Items) != 1 {
		t.Fatalf("unexpected second page total=%d items=%d", second.Total, len(second.Items))
	}
}
