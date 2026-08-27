package repository

import (
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/safety"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/telemetry"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/trip"
)

type DispatchCommit struct {
	Mission                mission.Mission
	Vehicle                fleet.Vehicle
	Trip                   trip.Trip
	Audit                  audit.Event
	ExpectedMissionVersion int64
	ExpectedVehicleVersion int64
}

type TripStartCommit struct {
	Trip                   trip.Trip
	Mission                mission.Mission
	Vehicle                fleet.Vehicle
	Audit                  audit.Event
	ExpectedTripVersion    int64
	ExpectedMissionVersion int64
	ExpectedVehicleVersion int64
}

type TripCompletionCommit struct {
	Trip                   trip.Trip
	Mission                mission.Mission
	Vehicle                fleet.Vehicle
	Audit                  audit.Event
	Outbox                 job.Outbox
	ExpectedTripVersion    int64
	ExpectedMissionVersion int64
	ExpectedVehicleVersion int64
}

type MissionCancellationCommit struct {
	Audit                   audit.Event
	ExpectedMissionVersion  int64
	CancelledAt             time.Time
}

type TelemetryCommit struct {
	Sample   telemetry.Sample
	Incident *safety.Incident
	Outbox   *job.Outbox
	Audit    *audit.Event
}

type IncidentResolutionCommit struct {
	Incident                safety.Incident
	Audit                   audit.Event
	VehicleStatus           fleet.VehicleStatus
	ExpectedIncidentVersion int64
}
