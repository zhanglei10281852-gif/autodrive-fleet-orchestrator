package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
)

type blockingFleetRepository struct {
	vehicle       fleet.Vehicle
	updateStarted chan struct{}
	releaseUpdate chan struct{}
	updateErr     error
	startedOnce   sync.Once
}

func (r *blockingFleetRepository) CreateRegion(context.Context, fleet.Region) error { return nil }
func (r *blockingFleetRepository) RegionByID(context.Context, string) (fleet.Region, error) {
	return fleet.Region{}, common.ErrNotFound
}
func (r *blockingFleetRepository) UpdateRegion(context.Context, fleet.Region, int64) error {
	return nil
}
func (r *blockingFleetRepository) RegionVehicleCount(context.Context, string) (int, error) {
	return 0, nil
}
func (r *blockingFleetRepository) CreateVehicle(context.Context, fleet.Vehicle) error { return nil }
func (r *blockingFleetRepository) VehicleByID(context.Context, string) (fleet.Vehicle, error) {
	return r.vehicle, nil
}
func (r *blockingFleetRepository) ListVehicles(context.Context, fleet.Filter) (common.Page[fleet.Vehicle], error) {
	return common.Page[fleet.Vehicle]{}, nil
}
func (r *blockingFleetRepository) UpdateVehicle(context.Context, fleet.Vehicle, int64) error {
	r.startedOnce.Do(func() { close(r.updateStarted) })
	<-r.releaseUpdate
	return r.updateErr
}
func (r *blockingFleetRepository) SetVehicleSafetyValidity(context.Context, string, time.Time, time.Time, int64) error {
	return nil
}

func TestVehicleTransitionWaitsForPersistenceResult(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("database write rejected")
	repository := &blockingFleetRepository{
		vehicle:       fleet.Vehicle{ID: "vehicle-1", RegionID: "region-1", Status: fleet.VehicleDraft, Version: 1},
		updateStarted: make(chan struct{}), releaseUpdate: make(chan struct{}), updateErr: writeErr,
	}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(repository.releaseUpdate) })
	service := NewFleet(repository, clock.System{}, idgen.NewSequence(1))
	principal := auth.Principal{UserID: "admin-1", Role: auth.RoleFleetAdmin}
	type result struct {
		vehicle fleet.Vehicle
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		vehicle, err := service.TransitionVehicle(context.Background(), principal, "vehicle-1", fleet.VehicleOffline, 1)
		resultCh <- result{vehicle: vehicle, err: err}
	}()

	select {
	case <-repository.updateStarted:
	case <-time.After(time.Second):
		t.Fatal("vehicle persistence did not start")
	}
	select {
	case got := <-resultCh:
		t.Fatalf("transition returned before persistence finished: vehicle=%+v err=%v", got.vehicle, got.err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(repository.releaseUpdate) })
	select {
	case got := <-resultCh:
		if !errors.Is(got.err, writeErr) {
			t.Fatalf("transition error=%v, want persistence error", got.err)
		}
		if got.vehicle.ID != "" {
			t.Fatalf("failed transition returned vehicle=%+v", got.vehicle)
		}
	case <-time.After(time.Second):
		t.Fatal("transition did not return persistence result")
	}
}
