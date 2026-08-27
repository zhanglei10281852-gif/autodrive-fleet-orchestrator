package service_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

func TestMissionCancellationAuditFailureRollsBackState(t *testing.T) {
	ctx := request.WithID(context.Background(), "req-cancel-private")
	now := time.Date(2026, 8, 27, 6, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "mission-cancel.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	stamp := now.Format(time.RFC3339Nano)
	earliest := now.Add(time.Hour).Format(time.RFC3339Nano)
	deadline := now.Add(2 * time.Hour).Format(time.RFC3339Nano)
	seed := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users(id, username, password_hash, role, active, created_at, updated_at) VALUES(?, ?, ?, 'dispatcher', 1, ?, ?)`, []any{"usr_cancel", "mission-canceller", "hash", stamp, stamp}},
		{`INSERT INTO regions(id, code, name, timezone, status, max_vehicles, version, created_at, updated_at) VALUES(?, ?, ?, 'UTC', 'active', 10, 1, ?, ?)`, []any{"reg_cancel", "CANCEL", "Cancel Region", stamp, stamp}},
		{`INSERT INTO missions(id, region_id, external_reference, idempotency_key, kind, priority, status, pickup_latitude, pickup_longitude, dropoff_latitude, dropoff_longitude, earliest_start_at, deadline_at, minimum_battery, required_capability, version, created_by, created_at, updated_at) VALUES(?, ?, ?, ?, 'passenger', 'routine', 'pending', 1, 1, 2, 2, ?, ?, 20, 'passenger', 1, ?, ?, ?)`, []any{"mis_cancel_fail", "reg_cancel", "ext-cancel-fail", "idem-cancel-fail", earliest, deadline, "usr_cancel", stamp, stamp}},
		{`INSERT INTO missions(id, region_id, external_reference, idempotency_key, kind, priority, status, pickup_latitude, pickup_longitude, dropoff_latitude, dropoff_longitude, earliest_start_at, deadline_at, minimum_battery, required_capability, version, created_by, created_at, updated_at) VALUES(?, ?, ?, ?, 'passenger', 'routine', 'pending', 1, 1, 2, 2, ?, ?, 20, 'passenger', 1, ?, ?, ?)`, []any{"mis_cancel_ok", "reg_cancel", "ext-cancel-ok", "idem-cancel-ok", earliest, deadline, "usr_cancel", stamp, stamp}},
		{`INSERT INTO audit_events(id, actor_id, actor_role, action, object_type, object_id, result, request_id, details, created_at) VALUES(?, ?, 'dispatcher', 'seed.block', 'mission', ?, 'success', ?, '{}', ?)`, []any{"aud_00001000", "usr_cancel", "mis_cancel_fail", "req-seed", stamp}},
	}
	for _, statement := range seed {
		if _, err := store.DB().ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed cancellation scenario: %v", err)
		}
	}

	dispatch := service.NewDispatch(store, clock.NewManual(now), idgen.NewSequence(1000))
	principal := auth.Principal{UserID: "usr_cancel", Username: "mission-canceller", Role: auth.RoleDispatcher, SessionID: "ses_cancel"}
	if err := dispatch.CancelMission(ctx, principal, "mis_cancel_fail", 1); err == nil {
		t.Fatal("cancellation unexpectedly succeeded despite duplicate audit id")
	}
	failedMission, err := store.MissionByID(ctx, "mis_cancel_fail")
	if err != nil {
		t.Fatalf("read failed cancellation mission: %v", err)
	}
	if failedMission.Status != mission.StatusPending || failedMission.Version != 1 {
		t.Fatalf("audit failure leaked mission cancellation: %+v", failedMission)
	}
	var failedAuditCount int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action = 'mission.cancel' AND object_id = ?`, "mis_cancel_fail").Scan(&failedAuditCount); err != nil {
		t.Fatalf("count failed cancellation audits: %v", err)
	}
	if failedAuditCount != 0 {
		t.Fatalf("failed cancellation wrote %d mission.cancel audits", failedAuditCount)
	}

	if err := dispatch.CancelMission(ctx, principal, "mis_cancel_ok", 1); err != nil {
		t.Fatalf("ordinary cancellation failed: %v", err)
	}
	cancelled, err := store.MissionByID(ctx, "mis_cancel_ok")
	if err != nil || cancelled.Status != mission.StatusCancelled || cancelled.Version != 2 {
		t.Fatalf("ordinary cancellation state=%+v err=%v", cancelled, err)
	}
	var successAuditCount int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE action = 'mission.cancel' AND object_id = ?`, "mis_cancel_ok").Scan(&successAuditCount); err != nil {
		t.Fatalf("count successful cancellation audits: %v", err)
	}
	if successAuditCount != 1 {
		t.Fatalf("ordinary cancellation audit count=%d, want 1", successAuditCount)
	}
}
