package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/charging"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/maintenance"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
)

type ResourceRepository interface {
	VehicleByID(context.Context, string) (fleet.Vehicle, error)
	CreateStation(context.Context, charging.Station) error
	CreateConnector(context.Context, charging.Connector) error
	ConnectorByID(context.Context, string) (charging.Connector, error)
	CreateChargingSession(context.Context, charging.Session, audit.Event) error
	ChargingSessionByID(context.Context, string) (charging.Session, error)
	ChargingSessionByIdempotency(context.Context, string, string) (charging.Session, error)
	StartCharging(context.Context, charging.Session, charging.Connector, int64, time.Time, audit.Event) error
	CompleteCharging(context.Context, charging.Session, int64, time.Time, audit.Event, job.Outbox) error
	OpenMaintenance(context.Context, maintenance.WorkOrder, fleet.Vehicle, audit.Event) error
	LockVehicleForMaintenance(context.Context, fleet.Vehicle, time.Time) error
	CreateMaintenanceOrder(context.Context, maintenance.WorkOrder, audit.Event) error
	MaintenanceByID(context.Context, string) (maintenance.WorkOrder, error)
	UpdateMaintenance(context.Context, maintenance.WorkOrder, int64) error
	CompleteMaintenance(context.Context, maintenance.WorkOrder, fleet.Vehicle, fleet.VehicleStatus, time.Time, audit.Event) error
}

type ResourceService struct {
	repository ResourceRepository
	clock      clock.Clock
	ids        idgen.Generator
}

func NewResource(repository ResourceRepository, businessClock clock.Clock, ids idgen.Generator) *ResourceService {
	return &ResourceService{repository: repository, clock: businessClock, ids: ids}
}

func (s *ResourceService) CreateStation(ctx context.Context, principal auth.Principal, regionID, code, name string) (charging.Station, error) {
	if err := principal.Require(auth.RoleFleetAdmin); err != nil {
		return charging.Station{}, err
	}
	id, err := s.ids.New("stn")
	if err != nil {
		return charging.Station{}, err
	}
	now := s.clock.Now()
	station := charging.Station{ID: id, RegionID: regionID, Code: strings.ToUpper(strings.TrimSpace(code)), Name: strings.TrimSpace(name), Active: true, CreatedAt: now, UpdatedAt: now}
	if station.RegionID == "" || station.Code == "" || station.Name == "" {
		return charging.Station{}, common.ErrInvalid
	}
	if err := s.repository.CreateStation(ctx, station); err != nil {
		return charging.Station{}, err
	}
	return station, nil
}

func (s *ResourceService) CreateConnector(ctx context.Context, principal auth.Principal, stationID, code string, powerKW int) (charging.Connector, error) {
	if err := principal.Require(auth.RoleFleetAdmin); err != nil {
		return charging.Connector{}, err
	}
	id, err := s.ids.New("con")
	if err != nil {
		return charging.Connector{}, err
	}
	connector := charging.Connector{ID: id, StationID: stationID, Code: strings.ToUpper(strings.TrimSpace(code)), PowerKW: powerKW, Active: true, Version: 1}
	if err := connector.Validate(); err != nil {
		return charging.Connector{}, err
	}
	if err := s.repository.CreateConnector(ctx, connector); err != nil {
		return charging.Connector{}, err
	}
	return connector, nil
}

type ReserveChargingInput struct {
	VehicleID      string    `json:"vehicle_id"`
	ConnectorID    string    `json:"connector_id"`
	WindowStart    time.Time `json:"window_start"`
	WindowEnd      time.Time `json:"window_end"`
	IdempotencyKey string    `json:"idempotency_key"`
}

func (s *ResourceService) ReserveCharging(ctx context.Context, principal auth.Principal, input ReserveChargingInput) (charging.Session, error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleFleetAdmin); err != nil {
		return charging.Session{}, err
	}
	existing, err := s.repository.ChargingSessionByIdempotency(ctx, principal.UserID, input.IdempotencyKey)
	if err == nil {
		if existing.VehicleID == input.VehicleID && existing.ConnectorID == input.ConnectorID && existing.WindowStart.Equal(input.WindowStart) && existing.WindowEnd.Equal(input.WindowEnd) {
			return existing, nil
		}
		return charging.Session{}, common.ErrConflict
	}
	if !errors.Is(err, common.ErrNotFound) {
		return charging.Session{}, err
	}
	vehicle, err := s.repository.VehicleByID(ctx, input.VehicleID)
	if err != nil {
		return charging.Session{}, err
	}
	connector, err := s.repository.ConnectorByID(ctx, input.ConnectorID)
	if err != nil {
		return charging.Session{}, err
	}
	if !connector.Active || vehicle.Status != fleet.VehicleAvailable {
		return charging.Session{}, common.ErrConflict
	}
	id, err := s.ids.New("chg")
	if err != nil {
		return charging.Session{}, err
	}
	now := s.clock.Now()
	session := charging.Session{ID: id, VehicleID: vehicle.ID, ConnectorID: connector.ID, Status: charging.StatusReserved,
		WindowStart: input.WindowStart, WindowEnd: input.WindowEnd, InitialBattery: vehicle.BatteryPercent,
		IdempotencyKey: input.IdempotencyKey, Version: 1, CreatedBy: principal.UserID, CreatedAt: now}
	if err := session.Validate(now); err != nil {
		return charging.Session{}, err
	}
	event, err := s.audit(ctx, principal, "charging.reserve", "charging_session", id, map[string]any{"vehicle_id": vehicle.ID, "connector_id": connector.ID})
	if err != nil {
		return charging.Session{}, err
	}
	if err := s.repository.CreateChargingSession(ctx, session, event); err != nil {
		return charging.Session{}, err
	}
	return session, nil
}

func (s *ResourceService) StartCharging(ctx context.Context, principal auth.Principal, id string, expectedVersion int64) (charging.Session, error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleFleetAdmin); err != nil {
		return charging.Session{}, err
	}
	session, err := s.repository.ChargingSessionByID(ctx, id)
	if err != nil {
		return charging.Session{}, err
	}
	if session.Version != expectedVersion {
		return charging.Session{}, common.ErrConflict
	}
	vehicle, err := s.repository.VehicleByID(ctx, session.VehicleID)
	if err != nil {
		return charging.Session{}, err
	}
	connector, err := s.repository.ConnectorByID(ctx, session.ConnectorID)
	if err != nil {
		return charging.Session{}, err
	}
	now := s.clock.Now()
	started, err := session.Start(now)
	if err != nil {
		return charging.Session{}, err
	}
	event, err := s.audit(ctx, principal, "charging.start", "charging_session", id, map[string]any{"vehicle_version": vehicle.Version, "connector_version": connector.Version})
	if err != nil {
		return charging.Session{}, err
	}
	if err := s.repository.StartCharging(ctx, started, connector, vehicle.Version, now, event); err != nil {
		return charging.Session{}, err
	}
	return started, nil
}

func (s *ResourceService) CompleteCharging(ctx context.Context, principal auth.Principal, id string, expectedVersion int64, finalBattery int, energy int64) (charging.Session, error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleFleetAdmin); err != nil {
		return charging.Session{}, err
	}
	session, err := s.repository.ChargingSessionByID(ctx, id)
	if err != nil {
		return charging.Session{}, err
	}
	if session.Version != expectedVersion {
		return charging.Session{}, common.ErrConflict
	}
	vehicle, err := s.repository.VehicleByID(ctx, session.VehicleID)
	if err != nil {
		return charging.Session{}, err
	}
	now := s.clock.Now()
	completed, err := session.Complete(now, finalBattery, energy)
	if err != nil {
		return charging.Session{}, err
	}
	event, err := s.audit(ctx, principal, "charging.complete", "charging_session", id, map[string]any{"battery_percent": finalBattery, "energy_watt_hours": energy})
	if err != nil {
		return charging.Session{}, err
	}
	outboxID, err := s.ids.New("evt")
	if err != nil {
		return charging.Session{}, err
	}
	payload, _ := json.Marshal(map[string]any{"session_id": id, "vehicle_id": vehicle.ID, "final_battery": finalBattery})
	outbox := job.Outbox{ID: outboxID, Topic: "charging.completed", AggregateType: "charging_session", AggregateID: id,
		Payload: payload, Status: job.StatusPending, MaxAttempts: 5, AvailableAt: now, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.CompleteCharging(ctx, completed, vehicle.Version, now, event, outbox); err != nil {
		return charging.Session{}, err
	}
	return completed, nil
}

type OpenMaintenanceInput struct {
	VehicleID      string   `json:"vehicle_id"`
	Reason         string   `json:"reason"`
	Priority       string   `json:"priority"`
	RequiredChecks []string `json:"required_checks"`
}

func (s *ResourceService) OpenMaintenance(ctx context.Context, principal auth.Principal, input OpenMaintenanceInput) (maintenance.WorkOrder, error) {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return maintenance.WorkOrder{}, err
	}
	vehicle, err := s.repository.VehicleByID(ctx, input.VehicleID)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	if vehicle.Status != fleet.VehicleAvailable && vehicle.Status != fleet.VehicleOffline {
		return maintenance.WorkOrder{}, common.ConflictError{Resource: "vehicle", Reason: "active trip or resource ownership prevents maintenance"}
	}
	id, err := s.ids.New("mnt")
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	now := s.clock.Now()
	order := maintenance.WorkOrder{ID: id, VehicleID: vehicle.ID, Status: maintenance.StatusOpen,
		Reason: input.Reason, Priority: input.Priority, PreviousVehicleStatus: string(vehicle.Status),
		RequiredChecks: append([]string(nil), input.RequiredChecks...), CompletedChecks: []string{},
		Version: 1, CreatedBy: principal.UserID, CreatedAt: now}
	if err := order.Validate(); err != nil {
		return maintenance.WorkOrder{}, err
	}
	event, err := s.audit(ctx, principal, "maintenance.open", "maintenance_order", id, map[string]any{"vehicle_id": vehicle.ID, "previous_status": vehicle.Status})
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	if err := s.repository.LockVehicleForMaintenance(ctx, vehicle, now); err != nil {
		return maintenance.WorkOrder{}, err
	}
	if err := s.repository.CreateMaintenanceOrder(ctx, order, event); err != nil {
		return maintenance.WorkOrder{}, err
	}
	return order, nil
}

func (s *ResourceService) StartMaintenance(ctx context.Context, principal auth.Principal, id, technician string, expectedVersion int64) (maintenance.WorkOrder, error) {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return maintenance.WorkOrder{}, err
	}
	order, err := s.repository.MaintenanceByID(ctx, id)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	if order.Version != expectedVersion {
		return maintenance.WorkOrder{}, common.ErrConflict
	}
	started, err := order.Start(technician, s.clock.Now())
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	if err := s.repository.UpdateMaintenance(ctx, started, expectedVersion); err != nil {
		return maintenance.WorkOrder{}, err
	}
	return started, nil
}

func (s *ResourceService) RecordMaintenanceCheck(ctx context.Context, principal auth.Principal, id, check string, expectedVersion int64) (maintenance.WorkOrder, error) {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return maintenance.WorkOrder{}, err
	}
	order, err := s.repository.MaintenanceByID(ctx, id)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	if order.Version != expectedVersion {
		return maintenance.WorkOrder{}, common.ErrConflict
	}
	updated, err := order.RecordCheck(check)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	if err := s.repository.UpdateMaintenance(ctx, updated, expectedVersion); err != nil {
		return maintenance.WorkOrder{}, err
	}
	return updated, nil
}

func (s *ResourceService) CompleteMaintenance(ctx context.Context, principal auth.Principal, id, resolution string, expectedVersion int64) (maintenance.WorkOrder, error) {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return maintenance.WorkOrder{}, err
	}
	order, err := s.repository.MaintenanceByID(ctx, id)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	if order.Version != expectedVersion {
		return maintenance.WorkOrder{}, common.ErrConflict
	}
	vehicle, err := s.repository.VehicleByID(ctx, order.VehicleID)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	completed, err := order.Complete(resolution, s.clock.Now())
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	restore := fleet.VehicleStatus(order.PreviousVehicleStatus)
	if restore != fleet.VehicleAvailable && restore != fleet.VehicleOffline {
		restore = fleet.VehicleOffline
	}
	event, err := s.audit(ctx, principal, "maintenance.complete", "maintenance_order", id, map[string]any{"vehicle_id": vehicle.ID, "restore_status": restore})
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	if err := s.repository.CompleteMaintenance(ctx, completed, vehicle, restore, s.clock.Now(), event); err != nil {
		return maintenance.WorkOrder{}, err
	}
	return completed, nil
}

func (s *ResourceService) audit(ctx context.Context, principal auth.Principal, action, objectType, objectID string, details any) (audit.Event, error) {
	id, err := s.ids.New("aud")
	if err != nil {
		return audit.Event{}, err
	}
	encoded, err := audit.Details(details)
	if err != nil {
		return audit.Event{}, err
	}
	return audit.Event{ID: id, ActorID: principal.UserID, ActorRole: string(principal.Role), Action: action,
		ObjectType: objectType, ObjectID: objectID, Result: audit.ResultSuccess, RequestID: requestIDOrInternal(ctx),
		Details: encoded, CreatedAt: s.clock.Now()}, nil
}
