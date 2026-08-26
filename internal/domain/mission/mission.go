package mission

import (
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusAssigned   Status = "assigned"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusCancelled  Status = "cancelled"
	StatusFailed     Status = "failed"
)

type Priority string

const (
	PriorityRoutine  Priority = "routine"
	PriorityUrgent   Priority = "urgent"
	PriorityCritical Priority = "critical"
)

type Mission struct {
	ID                 string    `json:"id"`
	RegionID           string    `json:"region_id"`
	ExternalReference  string    `json:"external_reference"`
	IdempotencyKey     string    `json:"idempotency_key"`
	Kind               string    `json:"kind"`
	Priority           Priority  `json:"priority"`
	Status             Status    `json:"status"`
	PickupLatitude     float64   `json:"pickup_latitude"`
	PickupLongitude    float64   `json:"pickup_longitude"`
	DropoffLatitude    float64   `json:"dropoff_latitude"`
	DropoffLongitude   float64   `json:"dropoff_longitude"`
	EarliestStartAt    time.Time `json:"earliest_start_at"`
	DeadlineAt         time.Time `json:"deadline_at"`
	MinimumBattery     int       `json:"minimum_battery"`
	RequiredCapability string    `json:"required_capability"`
	AssignedVehicleID  string    `json:"assigned_vehicle_id,omitempty"`
	Version            int64     `json:"version"`
	CreatedBy          string    `json:"created_by"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (m Mission) Validate(now time.Time) error {
	if m.ID == "" || m.RegionID == "" || m.CreatedBy == "" {
		return fmt.Errorf("mission identity, region, and creator are required: %w", common.ErrInvalid)
	}
	if strings.TrimSpace(m.IdempotencyKey) == "" {
		return common.FieldError{Field: "idempotency_key", Problem: "is required"}
	}
	if strings.TrimSpace(m.Kind) == "" || strings.TrimSpace(m.RequiredCapability) == "" {
		return common.FieldError{Field: "kind", Problem: "kind and required capability are required"}
	}
	if m.MinimumBattery < 5 || m.MinimumBattery > 100 {
		return common.FieldError{Field: "minimum_battery", Problem: "must be between 5 and 100"}
	}
	if !m.DeadlineAt.After(m.EarliestStartAt) || !m.DeadlineAt.After(now) {
		return common.FieldError{Field: "deadline_at", Problem: "must follow earliest start and be in the future"}
	}
	if !validCoordinates(m.PickupLatitude, m.PickupLongitude) || !validCoordinates(m.DropoffLatitude, m.DropoffLongitude) {
		return common.FieldError{Field: "route", Problem: "contains invalid coordinates"}
	}
	return nil
}

func (m Mission) CanAssign(now time.Time) error {
	if m.Status != StatusPending {
		return common.ConflictError{Resource: "mission", Reason: "mission is not pending"}
	}
	if now.After(m.DeadlineAt) {
		return common.ConflictError{Resource: "mission", Reason: "mission deadline passed"}
	}
	return nil
}

func (m Mission) Assign(vehicleID string) (Mission, error) {
	if m.Status != StatusPending || vehicleID == "" {
		return Mission{}, common.TransitionError{Entity: "mission", From: string(m.Status), To: string(StatusAssigned), Reason: "pending mission and vehicle are required"}
	}
	m.Status = StatusAssigned
	m.AssignedVehicleID = vehicleID
	m.Version++
	return m, nil
}

func (m Mission) Transition(to Status) (Mission, error) {
	allowed := map[Status]map[Status]bool{
		StatusPending:    {StatusAssigned: true, StatusCancelled: true},
		StatusAssigned:   {StatusInProgress: true, StatusPending: true, StatusCancelled: true, StatusFailed: true},
		StatusInProgress: {StatusCompleted: true, StatusFailed: true, StatusCancelled: true},
		StatusCompleted:  {},
		StatusCancelled:  {},
		StatusFailed:     {},
	}
	if !allowed[m.Status][to] {
		return Mission{}, common.TransitionError{Entity: "mission", From: string(m.Status), To: string(to), Reason: "transition is not permitted"}
	}
	m.Status = to
	if to == StatusPending {
		m.AssignedVehicleID = ""
	}
	m.Version++
	return m, nil
}

type Filter struct {
	RegionID string
	Status   Status
	Priority Priority
	From     *time.Time
	To       *time.Time
	Page     common.PageRequest
}

func validCoordinates(latitude, longitude float64) bool {
	return latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180
}
