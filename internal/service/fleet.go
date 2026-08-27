package service

import (
	"context"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
)

type FleetRepository interface {
	CreateRegion(context.Context, fleet.Region) error
	RegionByID(context.Context, string) (fleet.Region, error)
	UpdateRegion(context.Context, fleet.Region, int64) error
	RegionVehicleCount(context.Context, string) (int, error)
	CreateVehicle(context.Context, fleet.Vehicle) error
	VehicleByID(context.Context, string) (fleet.Vehicle, error)
	ListVehicles(context.Context, fleet.Filter) (common.Page[fleet.Vehicle], error)
	UpdateVehicle(context.Context, fleet.Vehicle, int64) error
	SetVehicleSafetyValidity(context.Context, string, time.Time, time.Time, int64) error
}

type FleetService struct {
	repository FleetRepository
	clock      clock.Clock
	ids        idgen.Generator
}

func NewFleet(repository FleetRepository, businessClock clock.Clock, ids idgen.Generator) *FleetService {
	return &FleetService{repository: repository, clock: businessClock, ids: ids}
}

type CreateRegionInput struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Timezone    string `json:"timezone"`
	MaxVehicles int    `json:"max_vehicles"`
}

func (s *FleetService) CreateRegion(ctx context.Context, principal auth.Principal, input CreateRegionInput) (fleet.Region, error) {
	if err := principal.Require(auth.RoleFleetAdmin); err != nil {
		return fleet.Region{}, err
	}
	id, err := s.ids.New("reg")
	if err != nil {
		return fleet.Region{}, err
	}
	now := s.clock.Now()
	region := fleet.Region{
		ID: id, Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
		Timezone: input.Timezone, Status: fleet.RegionDraft, MaxVehicles: input.MaxVehicles,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := region.Validate(); err != nil {
		return fleet.Region{}, err
	}
	if err := s.repository.CreateRegion(ctx, region); err != nil {
		return fleet.Region{}, err
	}
	return region, nil
}

func (s *FleetService) TransitionRegion(ctx context.Context, principal auth.Principal, id string, to fleet.RegionStatus, expectedVersion int64) (fleet.Region, error) {
	if err := principal.Require(auth.RoleFleetAdmin); err != nil {
		return fleet.Region{}, err
	}
	current, err := s.repository.RegionByID(ctx, id)
	if err != nil {
		return fleet.Region{}, err
	}
	if current.Version != expectedVersion {
		return fleet.Region{}, common.ErrConflict
	}
	updated, err := current.Transition(to)
	if err != nil {
		return fleet.Region{}, err
	}
	updated.UpdatedAt = s.clock.Now()
	if err := s.repository.UpdateRegion(ctx, updated, expectedVersion); err != nil {
		return fleet.Region{}, err
	}
	return updated, nil
}

type RegisterVehicleInput struct {
	RegionID    string `json:"region_id"`
	VIN         string `json:"vin"`
	FleetNumber string `json:"fleet_number"`
	Capability  string `json:"capability"`
}

func (s *FleetService) RegisterVehicle(ctx context.Context, principal auth.Principal, input RegisterVehicleInput) (fleet.Vehicle, error) {
	if err := principal.Require(auth.RoleFleetAdmin); err != nil {
		return fleet.Vehicle{}, err
	}
	region, err := s.repository.RegionByID(ctx, input.RegionID)
	if err != nil {
		return fleet.Vehicle{}, err
	}
	count, err := s.repository.RegionVehicleCount(ctx, region.ID)
	if err != nil {
		return fleet.Vehicle{}, err
	}
	if err := region.CanAcceptVehicle(count); err != nil {
		return fleet.Vehicle{}, err
	}
	id, err := s.ids.New("veh")
	if err != nil {
		return fleet.Vehicle{}, err
	}
	now := s.clock.Now()
	vehicle := fleet.Vehicle{
		ID: id, RegionID: region.ID, VIN: strings.ToUpper(strings.TrimSpace(input.VIN)),
		FleetNumber: strings.ToUpper(strings.TrimSpace(input.FleetNumber)), Status: fleet.VehicleDraft,
		Capability: strings.TrimSpace(input.Capability), BatteryPercent: 100, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := vehicle.Validate(); err != nil {
		return fleet.Vehicle{}, err
	}
	if err := s.repository.CreateVehicle(ctx, vehicle); err != nil {
		return fleet.Vehicle{}, err
	}
	return vehicle, nil
}

func (s *FleetService) TransitionVehicle(ctx context.Context, principal auth.Principal, id string, to fleet.VehicleStatus, expectedVersion int64) (fleet.Vehicle, error) {
	if err := principal.Require(auth.RoleFleetAdmin, auth.RoleSafetyOperator); err != nil {
		return fleet.Vehicle{}, err
	}
	current, err := s.repository.VehicleByID(ctx, id)
	if err != nil {
		return fleet.Vehicle{}, err
	}
	if current.Version != expectedVersion {
		return fleet.Vehicle{}, common.ErrConflict
	}
	if to == fleet.VehicleAvailable {
		region, err := s.repository.RegionByID(ctx, current.RegionID)
		if err != nil {
			return fleet.Vehicle{}, err
		}
		if region.Status != fleet.RegionActive {
			return fleet.Vehicle{}, common.ConflictError{Resource: "region", Reason: "inactive region cannot host an available vehicle"}
		}
		if current.SafetyValidUntil == nil || !current.SafetyValidUntil.After(s.clock.Now()) {
			return fleet.Vehicle{}, common.ConflictError{Resource: "vehicle", Reason: "valid safety inspection is required"}
		}
	}
	updated, err := current.Transition(to)
	if err != nil {
		return fleet.Vehicle{}, err
	}
	updated.UpdatedAt = s.clock.Now()
	if err := s.repository.UpdateVehicle(ctx, updated, expectedVersion); err != nil {
		return fleet.Vehicle{}, err
	}
	return updated, nil
}

func (s *FleetService) RecordSafetyInspection(ctx context.Context, principal auth.Principal, id string, validUntil time.Time, expectedVersion int64) error {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return err
	}
	if !validUntil.After(s.clock.Now().Add(time.Hour)) {
		return common.FieldError{Field: "valid_until", Problem: "must be at least one hour in the future"}
	}
	return s.repository.SetVehicleSafetyValidity(ctx, id, validUntil, s.clock.Now(), expectedVersion)
}

func (s *FleetService) ListVehicles(ctx context.Context, principal auth.Principal, filter fleet.Filter) (common.Page[fleet.Vehicle], error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return common.Page[fleet.Vehicle]{}, err
	}
	return s.repository.ListVehicles(ctx, filter)
}
