package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/auth"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/request"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/safety"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/telemetry"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/idgen"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/repository"
)

type OperationsRepository interface {
	VehicleByID(context.Context, string) (fleet.Vehicle, error)
	CommitTelemetry(context.Context, repository.TelemetryCommit) (bool, error)
	RecentTelemetry(context.Context, string, time.Time, int) ([]telemetry.Sample, error)
	IncidentByID(context.Context, string) (safety.Incident, error)
	ListIncidents(context.Context, safety.Filter) (common.Page[safety.Incident], error)
	ClaimIncident(context.Context, string, string, time.Time, time.Duration, int64) (safety.Incident, error)
	UpdateIncident(context.Context, safety.Incident, int64) error
	CommitIncidentResolution(context.Context, repository.IncidentResolutionCommit) error
}

type OperationsService struct {
	repository OperationsRepository
	clock      clock.Clock
	ids        idgen.Generator
	claimLease time.Duration
}

func NewOperations(repository OperationsRepository, businessClock clock.Clock, ids idgen.Generator, claimLease time.Duration) *OperationsService {
	return &OperationsService{repository: repository, clock: businessClock, ids: ids, claimLease: claimLease}
}

func (s *OperationsService) IngestTelemetry(ctx context.Context, samples []telemetry.Sample) telemetry.BatchResult {
	result := telemetry.NewBatchResult(len(samples))
	if len(samples) == 0 {
		return result
	}
	if len(samples) > 500 {
		for _, sample := range samples {
			result.Add(telemetry.IngestResult{EventID: sample.EventID, Code: "batch_too_large", Message: "batch cannot exceed 500 samples"})
		}
		return result
	}
	for _, sample := range samples {
		if err := ctx.Err(); err != nil {
			result.Add(telemetry.IngestResult{EventID: sample.EventID, Code: "cancelled", Message: err.Error()})
			continue
		}
		item := s.ingestOne(ctx, sample)
		result.Add(item)
	}
	return result
}

func (s *OperationsService) ingestOne(ctx context.Context, sample telemetry.Sample) telemetry.IngestResult {
	now := s.clock.Now()
	sample.ReceivedAt = now
	if err := sample.Validate(now); err != nil {
		return telemetry.IngestResult{EventID: sample.EventID, Code: "invalid_sample", Message: err.Error()}
	}
	vehicle, err := s.repository.VehicleByID(ctx, sample.VehicleID)
	if err != nil {
		return telemetry.IngestResult{EventID: sample.EventID, Code: "vehicle_not_found", Message: err.Error()}
	}
	sample.PayloadHash = hashTelemetry(sample)
	commit := repository.TelemetryCommit{Sample: sample}
	if sample.RaisesIncident() {
		incident, outbox, auditEvent, err := s.telemetryIncident(ctx, sample, vehicle)
		if err != nil {
			return telemetry.IngestResult{EventID: sample.EventID, Code: "incident_prepare_failed", Message: err.Error()}
		}
		commit.Incident = &incident
		commit.Outbox = &outbox
		commit.Audit = &auditEvent
	}
	duplicate, err := s.repository.CommitTelemetry(ctx, commit)
	if err != nil {
		code := "persistence_failed"
		if errors.Is(err, common.ErrConflict) {
			code = "event_conflict"
		}
		return telemetry.IngestResult{EventID: sample.EventID, Code: code, Message: err.Error()}
	}
	return telemetry.IngestResult{EventID: sample.EventID, Accepted: true, Duplicate: duplicate}
}

func (s *OperationsService) telemetryIncident(ctx context.Context, sample telemetry.Sample, vehicle fleet.Vehicle) (safety.Incident, job.Outbox, audit.Event, error) {
	incidentID, err := s.ids.New("inc")
	if err != nil {
		return safety.Incident{}, job.Outbox{}, audit.Event{}, err
	}
	outboxID, err := s.ids.New("evt")
	if err != nil {
		return safety.Incident{}, job.Outbox{}, audit.Event{}, err
	}
	auditID, err := s.ids.New("aud")
	if err != nil {
		return safety.Incident{}, job.Outbox{}, audit.Event{}, err
	}
	severity := safety.SeverityHigh
	if sample.Severity == telemetry.SeverityCritical {
		severity = safety.SeverityCritical
	}
	incident := safety.Incident{
		ID: incidentID, VehicleID: vehicle.ID, TelemetryEvent: sample.EventID,
		Severity: severity, Category: "telemetry_fault", Summary: fmt.Sprintf("vehicle reported fault %s", sample.FaultCode),
		Status: safety.StatusOpen, Version: 1, OpenedAt: sample.ReceivedAt, UpdatedAt: sample.ReceivedAt,
	}
	payload, _ := json.Marshal(map[string]any{"incident_id": incident.ID, "vehicle_id": vehicle.ID, "severity": severity})
	outbox := job.Outbox{
		ID: outboxID, Topic: "safety.incident.opened", AggregateType: "safety_incident",
		AggregateID: incident.ID, Payload: payload, Status: job.StatusPending, MaxAttempts: 8,
		AvailableAt: sample.ReceivedAt, CreatedAt: sample.ReceivedAt, UpdatedAt: sample.ReceivedAt,
	}
	details, _ := audit.Details(map[string]any{"event_id": sample.EventID, "fault_code": sample.FaultCode})
	auditEvent := audit.Event{
		ID: auditID, ActorID: "telemetry_gateway", ActorRole: "system", Action: "safety.incident.open",
		ObjectType: "safety_incident", ObjectID: incident.ID, Result: audit.ResultSuccess,
		RequestID: requestIDOrInternal(ctx), Details: details, CreatedAt: sample.ReceivedAt,
	}
	return incident, outbox, auditEvent, nil
}

func (s *OperationsService) ClaimIncident(ctx context.Context, principal auth.Principal, id string, expectedVersion int64) (safety.Incident, error) {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return safety.Incident{}, err
	}
	return s.repository.ClaimIncident(ctx, id, principal.UserID, s.clock.Now(), s.claimLease, expectedVersion)
}

func (s *OperationsService) StartMitigation(ctx context.Context, principal auth.Principal, id string, expectedVersion int64) (safety.Incident, error) {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return safety.Incident{}, err
	}
	current, err := s.repository.IncidentByID(ctx, id)
	if err != nil {
		return safety.Incident{}, err
	}
	if current.Version != expectedVersion {
		return safety.Incident{}, common.ErrConflict
	}
	updated, err := current.StartMitigation(principal.UserID, s.clock.Now())
	if err != nil {
		return safety.Incident{}, err
	}
	updated.UpdatedAt = s.clock.Now()
	if err := s.repository.UpdateIncident(ctx, updated, expectedVersion); err != nil {
		return safety.Incident{}, err
	}
	return updated, nil
}

func (s *OperationsService) ResolveIncident(ctx context.Context, principal auth.Principal, id, resolution string, expectedVersion int64) (safety.Incident, error) {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return safety.Incident{}, err
	}
	current, err := s.repository.IncidentByID(ctx, id)
	if err != nil {
		return safety.Incident{}, err
	}
	if current.Version != expectedVersion {
		return safety.Incident{}, common.ErrConflict
	}
	vehicle, err := s.repository.VehicleByID(ctx, current.VehicleID)
	if err != nil {
		return safety.Incident{}, err
	}
	vehicleSafe := vehicle.Status == fleet.VehicleSuspended || vehicle.Status == fleet.VehicleMaintenance || vehicle.Status == fleet.VehicleOffline
	resolved, err := current.Resolve(principal.UserID, resolution, vehicleSafe, s.clock.Now())
	if err != nil {
		return safety.Incident{}, err
	}
	auditID, err := s.ids.New("aud")
	if err != nil {
		return safety.Incident{}, err
	}
	details, _ := audit.Details(map[string]any{"vehicle_status": vehicle.Status, "resolution": resolution})
	auditEvent := audit.Event{
		ID: auditID, ActorID: principal.UserID, ActorRole: string(principal.Role), Action: "safety.incident.resolve",
		ObjectType: "safety_incident", ObjectID: id, Result: audit.ResultSuccess,
		RequestID: requestIDOrInternal(ctx), Details: details, CreatedAt: s.clock.Now(),
	}
	if err := s.repository.CommitIncidentResolution(ctx, repository.IncidentResolutionCommit{
		Incident: resolved, Audit: auditEvent, VehicleStatus: vehicle.Status,
		ExpectedIncidentVersion: expectedVersion,
	}); err != nil {
		return safety.Incident{}, err
	}
	return resolved, nil
}

func (s *OperationsService) CloseIncident(ctx context.Context, principal auth.Principal, id string, expectedVersion int64) (safety.Incident, error) {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return safety.Incident{}, err
	}
	current, err := s.repository.IncidentByID(ctx, id)
	if err != nil {
		return safety.Incident{}, err
	}
	if current.Version != expectedVersion {
		return safety.Incident{}, common.ErrConflict
	}
	closed, err := current.Close(s.clock.Now())
	if err != nil {
		return safety.Incident{}, err
	}
	closed.UpdatedAt = s.clock.Now()
	if err := s.repository.UpdateIncident(ctx, closed, expectedVersion); err != nil {
		return safety.Incident{}, err
	}
	return closed, nil
}

func (s *OperationsService) ListIncidents(ctx context.Context, principal auth.Principal, filter safety.Filter) (common.Page[safety.Incident], error) {
	if err := principal.Require(auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return common.Page[safety.Incident]{}, err
	}
	return s.repository.ListIncidents(ctx, filter)
}

func (s *OperationsService) RecentTelemetry(ctx context.Context, principal auth.Principal, vehicleID string, since time.Time, limit int) ([]telemetry.Sample, error) {
	if err := principal.Require(auth.RoleDispatcher, auth.RoleSafetyOperator, auth.RoleFleetAdmin); err != nil {
		return nil, err
	}
	return s.repository.RecentTelemetry(ctx, vehicleID, since, limit)
}

func hashTelemetry(sample telemetry.Sample) string {
	value := struct {
		VehicleID      string
		ObservedAt     time.Time
		Latitude       float64
		Longitude      float64
		SpeedKPH       float64
		BatteryPercent int
		OdometerMeters int64
		FaultCode      string
		Severity       telemetry.Severity
	}{sample.VehicleID, sample.ObservedAt, sample.Latitude, sample.Longitude, sample.SpeedKPH,
		sample.BatteryPercent, sample.OdometerMeters, sample.FaultCode, sample.Severity}
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func requestIDOrInternal(ctx context.Context) string {
	if value := request.ID(ctx); value != "" {
		return value
	}
	return "internal"
}
