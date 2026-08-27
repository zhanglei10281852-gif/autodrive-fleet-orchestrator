package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/trip"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/repository"
)

type DispatchRepository interface {
	CreateMission(context.Context, mission.Mission) error
	MissionByID(context.Context, string) (mission.Mission, error)
	MissionByIdempotency(context.Context, string, string) (mission.Mission, error)
	ListMissions(context.Context, mission.Filter) (common.Page[mission.Mission], error)
	VehicleByID(context.Context, string) (fleet.Vehicle, error)
	AvailableVehicleCandidates(context.Context, string, string, int, time.Time, int) ([]fleet.Vehicle, error)
	CommitDispatch(context.Context, repository.DispatchCommit) error
	TripByID(context.Context, string) (trip.Trip, error)
	TripByMissionID(context.Context, string) (trip.Trip, error)
	CommitTripStart(context.Context, repository.TripStartCommit) error
	CommitTripCompletion(context.Context, repository.TripCompletionCommit) error
	CancelPendingMission(context.Context, string, int64, time.Time, audit.Event) error
}

type DispatchService struct {
	repository DispatchRepository
	clock      clock.Clock
	ids        idgen.Generator
}

func NewDispatch(repository DispatchRepository, businessClock clock.Clock, ids idgen.Generator) *DispatchService {
	return &DispatchService{repository: repository, clock: businessClock, ids: ids}
}

type CreateMissionInput struct {
	RegionID           string           `json:"region_id"`
	ExternalReference  string           `json:"external_reference"`
	IdempotencyKey     string           `json:"idempotency_key"`
	Kind               string           `json:"kind"`
	Priority           mission.Priority `json:"priority"`
	PickupLatitude     float64          `json:"pickup_latitude"`
	PickupLongitude    float64          `json:"pickup_longitude"`
	DropoffLatitude    float64          `json:"dropoff_latitude"`
	DropoffLongitude   float64          `json:"dropoff_longitude"`
	EarliestStartAt    time.Time        `json:"earliest_start_at"`
	DeadlineAt         time.Time        `json:"deadline_at"`
	MinimumBattery     int              `json:"minimum_battery"`
	RequiredCapability string           `json:"required_capability"`
}

func (s *DispatchService) CreateMission(ctx context.Context, principal auth.Principal, input CreateMissionInput) (mission.Mission, error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleFleetAdmin); err != nil {
		return mission.Mission{}, err
	}
	if input.IdempotencyKey == "" {
		return mission.Mission{}, common.FieldError{Field: "idempotency_key", Problem: "is required"}
	}
	existing, err := s.repository.MissionByIdempotency(ctx, principal.UserID, input.IdempotencyKey)
	if err == nil {
		if missionInputMatches(existing, input) {
			return existing, nil
		}
		return mission.Mission{}, common.ConflictError{Resource: "idempotency_key", Reason: "key is bound to a different mission request"}
	}
	if !errors.Is(err, common.ErrNotFound) {
		return mission.Mission{}, err
	}
	id, err := s.ids.New("mis")
	if err != nil {
		return mission.Mission{}, err
	}
	now := s.clock.Now()
	value := mission.Mission{
		ID: id, RegionID: input.RegionID, ExternalReference: input.ExternalReference,
		IdempotencyKey: input.IdempotencyKey, Kind: input.Kind, Priority: input.Priority,
		Status: mission.StatusPending, PickupLatitude: input.PickupLatitude,
		PickupLongitude: input.PickupLongitude, DropoffLatitude: input.DropoffLatitude,
		DropoffLongitude: input.DropoffLongitude, EarliestStartAt: input.EarliestStartAt,
		DeadlineAt: input.DeadlineAt, MinimumBattery: input.MinimumBattery,
		RequiredCapability: input.RequiredCapability, Version: 1, CreatedBy: principal.UserID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := value.Validate(now); err != nil {
		return mission.Mission{}, err
	}
	if err := s.repository.CreateMission(ctx, value); err != nil {
		// Concurrent retries race the same idempotency key. Only the first
		// INSERT wins the UNIQUE(created_by, idempotency_key) constraint; the
		// rest fail with a conflict. To converge, every loser must re-read the
		// committed mission: identical content returns the same task, while
		// divergent content reusing the key stays rejected. The guard checks
		// errors.Is(ErrConflict) instead of the concrete WriteError so any
		// wrapper that unwraps to ErrConflict still reconciles.
		if errors.Is(err, common.ErrConflict) {
			if existing, getErr := s.repository.MissionByIdempotency(ctx, principal.UserID, input.IdempotencyKey); getErr == nil {
				if missionInputMatches(existing, input) {
					return existing, nil
				}
				return mission.Mission{}, common.ConflictError{Resource: "idempotency_key", Reason: "key is bound to a different mission request"}
			}
		}
		return mission.Mission{}, err
	}
	return value, nil
}

func missionInputMatches(existing mission.Mission, input CreateMissionInput) bool {
	return existing.RegionID == input.RegionID &&
		existing.ExternalReference == input.ExternalReference &&
		existing.Kind == input.Kind &&
		existing.Priority == input.Priority &&
		existing.PickupLatitude == input.PickupLatitude &&
		existing.PickupLongitude == input.PickupLongitude &&
		existing.DropoffLatitude == input.DropoffLatitude &&
		existing.DropoffLongitude == input.DropoffLongitude &&
		existing.EarliestStartAt.Equal(input.EarliestStartAt) &&
		existing.DeadlineAt.Equal(input.DeadlineAt) &&
		existing.MinimumBattery == input.MinimumBattery &&
		existing.RequiredCapability == input.RequiredCapability
}

func (s *DispatchService) Dispatch(ctx context.Context, principal auth.Principal, missionID string) (trip.Trip, error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleFleetAdmin); err != nil {
		return trip.Trip{}, err
	}
	if err := ctx.Err(); err != nil {
		return trip.Trip{}, err
	}
	now := s.clock.Now()
	target, err := s.repository.MissionByID(ctx, missionID)
	if err != nil {
		return trip.Trip{}, err
	}
	if err := target.CanAssign(now); err != nil {
		return trip.Trip{}, err
	}
	candidates, err := s.repository.AvailableVehicleCandidates(ctx, target.RegionID, target.RequiredCapability, target.MinimumBattery, now, 20)
	if err != nil {
		return trip.Trip{}, err
	}
	if len(candidates) == 0 {
		return trip.Trip{}, common.ConflictError{Resource: "dispatch", Reason: "no eligible vehicle is available"}
	}
	vehicle := candidates[0]
	if err := vehicle.CanDispatch(now, target.MinimumBattery); err != nil {
		return trip.Trip{}, err
	}
	assigned, err := target.Assign(vehicle.ID)
	if err != nil {
		return trip.Trip{}, err
	}
	reserved, err := vehicle.Transition(fleet.VehicleReserved)
	if err != nil {
		return trip.Trip{}, err
	}
	tripID, err := s.ids.New("trp")
	if err != nil {
		return trip.Trip{}, err
	}
	auditEvent, err := s.newAudit(ctx, principal, "mission.dispatch", "mission", target.ID,
		map[string]any{"vehicle_id": vehicle.ID, "mission_version": target.Version})
	if err != nil {
		return trip.Trip{}, err
	}
	assigned.UpdatedAt = now
	reserved.UpdatedAt = now
	created := trip.Trip{
		ID: tripID, MissionID: target.ID, VehicleID: vehicle.ID, Status: trip.StatusScheduled,
		ScheduledAt: maxTime(now, target.EarliestStartAt), Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := created.Validate(); err != nil {
		return trip.Trip{}, err
	}
	commit := repository.DispatchCommit{
		Mission: assigned, Vehicle: reserved, Trip: created, Audit: auditEvent,
		ExpectedMissionVersion: target.Version, ExpectedVehicleVersion: vehicle.Version,
	}
	if err := s.repository.CommitDispatch(ctx, commit); err != nil {
		return trip.Trip{}, err
	}
	return created, nil
}

func (s *DispatchService) StartTrip(ctx context.Context, principal auth.Principal, tripID string) (trip.Trip, error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleFleetAdmin); err != nil {
		return trip.Trip{}, err
	}
	current, err := s.repository.TripByID(ctx, tripID)
	if err != nil {
		return trip.Trip{}, err
	}
	target, err := s.repository.MissionByID(ctx, current.MissionID)
	if err != nil {
		return trip.Trip{}, err
	}
	vehicle, err := s.repository.VehicleByID(ctx, current.VehicleID)
	if err != nil {
		return trip.Trip{}, err
	}
	if target.Status != mission.StatusAssigned || target.AssignedVehicleID != vehicle.ID || vehicle.Status != fleet.VehicleReserved {
		return trip.Trip{}, common.ConflictError{Resource: "trip", Reason: "mission and vehicle assignment are inconsistent"}
	}
	now := s.clock.Now()
	startedTrip, err := current.Start(now)
	if err != nil {
		return trip.Trip{}, err
	}
	startedMission, err := target.Transition(mission.StatusInProgress)
	if err != nil {
		return trip.Trip{}, err
	}
	startedVehicle, err := vehicle.Transition(fleet.VehicleInTrip)
	if err != nil {
		return trip.Trip{}, err
	}
	startedTrip.UpdatedAt = now
	startedMission.UpdatedAt = now
	startedVehicle.UpdatedAt = now
	auditEvent, err := s.newAudit(ctx, principal, "trip.start", "trip", tripID, map[string]any{"mission_id": target.ID, "vehicle_id": vehicle.ID})
	if err != nil {
		return trip.Trip{}, err
	}
	if err := s.repository.CommitTripStart(ctx, repository.TripStartCommit{
		Trip: startedTrip, Mission: startedMission, Vehicle: startedVehicle, Audit: auditEvent,
		ExpectedTripVersion: current.Version, ExpectedMissionVersion: target.Version,
		ExpectedVehicleVersion: vehicle.Version,
	}); err != nil {
		return trip.Trip{}, err
	}
	return startedTrip, nil
}

func (s *DispatchService) CompleteTrip(ctx context.Context, principal auth.Principal, tripID string, distanceMeters int64) (trip.Trip, error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleFleetAdmin); err != nil {
		return trip.Trip{}, err
	}
	current, err := s.repository.TripByID(ctx, tripID)
	if err != nil {
		return trip.Trip{}, err
	}
	target, err := s.repository.MissionByID(ctx, current.MissionID)
	if err != nil {
		return trip.Trip{}, err
	}
	vehicle, err := s.repository.VehicleByID(ctx, current.VehicleID)
	if err != nil {
		return trip.Trip{}, err
	}
	if target.Status != mission.StatusInProgress || vehicle.Status != fleet.VehicleInTrip {
		return trip.Trip{}, common.ConflictError{Resource: "trip", Reason: "associated state is not in progress"}
	}
	now := s.clock.Now()
	completedTrip, err := current.Complete(now, distanceMeters)
	if err != nil {
		return trip.Trip{}, err
	}
	completedMission, err := target.Transition(mission.StatusCompleted)
	if err != nil {
		return trip.Trip{}, err
	}
	vehicleTarget := fleet.VehicleAvailable
	if vehicle.BatteryPercent < 20 {
		vehicleTarget = fleet.VehicleCharging
	}
	releasedVehicle, err := vehicle.Transition(vehicleTarget)
	if err != nil {
		return trip.Trip{}, err
	}
	completedTrip.UpdatedAt = now
	completedMission.UpdatedAt = now
	releasedVehicle.UpdatedAt = now
	auditEvent, err := s.newAudit(ctx, principal, "trip.complete", "trip", tripID,
		map[string]any{"distance_meters": distanceMeters, "vehicle_status": vehicleTarget})
	if err != nil {
		return trip.Trip{}, err
	}
	outboxID, err := s.ids.New("evt")
	if err != nil {
		return trip.Trip{}, err
	}
	payload, _ := json.Marshal(map[string]any{"trip_id": tripID, "mission_id": target.ID, "vehicle_id": vehicle.ID, "distance_meters": distanceMeters})
	outbox := job.Outbox{
		ID: outboxID, Topic: "trip.completed", AggregateType: "trip", AggregateID: tripID,
		Payload: payload, Status: job.StatusPending, MaxAttempts: 5, AvailableAt: now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repository.CommitTripCompletion(ctx, repository.TripCompletionCommit{
		Trip: completedTrip, Mission: completedMission, Vehicle: releasedVehicle,
		Audit: auditEvent, Outbox: outbox, ExpectedTripVersion: current.Version,
		ExpectedMissionVersion: target.Version, ExpectedVehicleVersion: vehicle.Version,
	}); err != nil {
		return trip.Trip{}, err
	}
	return completedTrip, nil
}

func (s *DispatchService) CancelMission(ctx context.Context, principal auth.Principal, missionID string, expectedVersion int64) error {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleFleetAdmin); err != nil {
		return err
	}
	target, err := s.repository.MissionByID(ctx, missionID)
	if err != nil {
		return err
	}
	if target.Version != expectedVersion || target.Status != mission.StatusPending {
		return common.ErrConflict
	}
	event, err := s.newAudit(ctx, principal, "mission.cancel", "mission", missionID, map[string]any{"version": expectedVersion})
	if err != nil {
		return err
	}
	return s.repository.CancelPendingMission(ctx, missionID, expectedVersion, s.clock.Now(), event)
}

func (s *DispatchService) ListMissions(ctx context.Context, principal auth.Principal, filter mission.Filter) (common.Page[mission.Mission], error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return common.Page[mission.Mission]{}, err
	}
	return s.repository.ListMissions(ctx, filter)
}

func (s *DispatchService) newAudit(ctx context.Context, principal auth.Principal, action, objectType, objectID string, details any) (audit.Event, error) {
	id, err := s.ids.New("aud")
	if err != nil {
		return audit.Event{}, err
	}
	encoded, err := audit.Details(details)
	if err != nil {
		return audit.Event{}, err
	}
	requestID := request.ID(ctx)
	if requestID == "" {
		requestID = "internal"
	}
	return audit.Event{ID: id, ActorID: principal.UserID, ActorRole: string(principal.Role), Action: action,
		ObjectType: objectType, ObjectID: objectID, Result: audit.ResultSuccess, RequestID: requestID,
		Details: encoded, CreatedAt: s.clock.Now()}, nil
}

func maxTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}
