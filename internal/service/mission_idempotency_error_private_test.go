package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/service"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/storage/sqlite"
)

type missionInitialLookupBarrier struct {
	*sqlite.Store
	arrivals atomic.Int32
	release  chan struct{}
}

func (r *missionInitialLookupBarrier) MissionByIdempotency(ctx context.Context, actorID, key string) (mission.Mission, error) {
	arrival := r.arrivals.Add(1)
	if arrival > 2 {
		return r.Store.MissionByIdempotency(ctx, actorID, key)
	}
	value, err := r.Store.MissionByIdempotency(ctx, actorID, key)
	if arrival == 2 {
		close(r.release)
	}
	<-r.release
	return value, err
}

func TestConcurrentMissionRetriesConvergeThroughWrappedConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	principal := auth.Principal{UserID: "dispatcher-1", Username: "dispatcher", Role: auth.RoleDispatcher, SessionID: "session-1"}
	user := auth.User{ID: principal.UserID, Username: principal.Username, PasswordHash: "stored-password-hash", Role: principal.Role, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("create dispatcher: %v", err)
	}
	region := fleet.Region{ID: "region-1", Code: "MISSION", Name: "Mission Region", Timezone: "Asia/Shanghai", Status: fleet.RegionActive, MaxVehicles: 20, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateRegion(ctx, region); err != nil {
		t.Fatalf("create region: %v", err)
	}
	repository := &missionInitialLookupBarrier{Store: store, release: make(chan struct{})}
	dispatch := service.NewDispatch(repository, clock.NewManual(now), idgen.NewSequence(1))
	input := service.CreateMissionInput{RegionID: region.ID, ExternalReference: "ride-900", IdempotencyKey: "retry-key-900", Kind: "passenger", Priority: mission.PriorityUrgent, PickupLatitude: 31.20, PickupLongitude: 121.40, DropoffLatitude: 31.30, DropoffLongitude: 121.50, EarliestStartAt: now.Add(5 * time.Minute), DeadlineAt: now.Add(time.Hour), MinimumBattery: 40, RequiredCapability: "passenger"}

	type outcome struct {
		mission mission.Mission
		err     error
	}
	start := make(chan struct{})
	results := make(chan outcome, 2)
	for range 2 {
		go func() {
			<-start
			value, err := dispatch.CreateMission(ctx, principal, input)
			results <- outcome{mission: value, err: err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("identical concurrent retries returned errors: first=%v second=%v", first.err, second.err)
	}
	if first.mission.ID == "" || first.mission.ID != second.mission.ID {
		t.Fatalf("retries did not converge: first=%+v second=%+v", first.mission, second.mission)
	}

	different := input
	different.ExternalReference = "ride-901"
	if _, err := dispatch.CreateMission(ctx, principal, different); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("different request reused key with error %v, want conflict", err)
	}
}
