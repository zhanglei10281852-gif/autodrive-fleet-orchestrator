package safety

import (
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type Status string

const (
	StatusOpen         Status = "open"
	StatusAcknowledged Status = "acknowledged"
	StatusMitigating   Status = "mitigating"
	StatusResolved     Status = "resolved"
	StatusClosed       Status = "closed"
)

type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type Incident struct {
	ID             string     `json:"id"`
	VehicleID      string     `json:"vehicle_id"`
	TelemetryEvent string     `json:"telemetry_event_id,omitempty"`
	Severity       Severity   `json:"severity"`
	Category       string     `json:"category"`
	Summary        string     `json:"summary"`
	Status         Status     `json:"status"`
	OwnerID        string     `json:"owner_id,omitempty"`
	LeaseUntil     *time.Time `json:"lease_until,omitempty"`
	Resolution     string     `json:"resolution,omitempty"`
	Version        int64      `json:"version"`
	OpenedAt       time.Time  `json:"opened_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
}

func (i Incident) Validate() error {
	if i.ID == "" || i.VehicleID == "" {
		return fmt.Errorf("incident and vehicle identity are required: %w", common.ErrInvalid)
	}
	if strings.TrimSpace(i.Category) == "" || len(strings.TrimSpace(i.Summary)) < 5 {
		return common.FieldError{Field: "summary", Problem: "category and meaningful summary are required"}
	}
	if !i.Severity.Valid() {
		return common.FieldError{Field: "severity", Problem: "is not supported"}
	}
	return nil
}

func (s Severity) Valid() bool {
	switch s {
	case SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical:
		return true
	default:
		return false
	}
}

func (i Incident) Claim(operatorID string, now time.Time, lease time.Duration) (Incident, error) {
	if operatorID == "" || lease <= 0 {
		return Incident{}, common.ErrInvalid
	}
	if i.Status == StatusClosed || i.Status == StatusResolved {
		return Incident{}, common.ConflictError{Resource: "incident", Reason: "incident is final"}
	}
	if i.OwnerID != "" && i.OwnerID != operatorID && i.LeaseUntil != nil && i.LeaseUntil.After(now) {
		return Incident{}, common.ConflictError{Resource: "incident", Reason: "incident has an active owner lease"}
	}
	until := now.Add(lease).UTC()
	i.OwnerID = operatorID
	i.LeaseUntil = &until
	if i.Status == StatusOpen {
		i.Status = StatusAcknowledged
	}
	i.Version++
	return i, nil
}

func (i Incident) StartMitigation(operatorID string, now time.Time) (Incident, error) {
	if i.Status != StatusAcknowledged || i.OwnerID != operatorID || i.LeaseUntil == nil || !i.LeaseUntil.After(now) {
		return Incident{}, common.ConflictError{Resource: "incident", Reason: "active ownership is required"}
	}
	i.Status = StatusMitigating
	i.Version++
	return i, nil
}

func (i Incident) Resolve(operatorID, resolution string, vehicleSafe bool, now time.Time) (Incident, error) {
	if i.Status != StatusMitigating || i.OwnerID != operatorID {
		return Incident{}, common.ConflictError{Resource: "incident", Reason: "mitigation owner is required"}
	}
	if !vehicleSafe {
		return Incident{}, common.ConflictError{Resource: "vehicle", Reason: "vehicle has not reached a safe state"}
	}
	if len(strings.TrimSpace(resolution)) < 10 {
		return Incident{}, common.FieldError{Field: "resolution", Problem: "must describe the completed mitigation"}
	}
	i.Status = StatusResolved
	i.Resolution = resolution
	i.LeaseUntil = nil
	i.Version++
	i.UpdatedAt = now.UTC()
	return i, nil
}

func (i Incident) Close(now time.Time) (Incident, error) {
	if i.Status != StatusResolved {
		return Incident{}, common.TransitionError{Entity: "incident", From: string(i.Status), To: string(StatusClosed), Reason: "resolution must be recorded first"}
	}
	i.Status = StatusClosed
	closed := now.UTC()
	i.ClosedAt = &closed
	i.Version++
	return i, nil
}

type Filter struct {
	VehicleID string
	Status    Status
	Severity  Severity
	OwnerID   string
	Page      common.PageRequest
}
