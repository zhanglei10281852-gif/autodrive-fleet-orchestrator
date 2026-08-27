package trip

import (
	"fmt"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type Status string

const (
	StatusScheduled Status = "scheduled"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusAborted   Status = "aborted"
)

type Trip struct {
	ID             string     `json:"id"`
	MissionID      string     `json:"mission_id"`
	VehicleID      string     `json:"vehicle_id"`
	Status         Status     `json:"status"`
	ScheduledAt    time.Time  `json:"scheduled_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	AbortReason    string     `json:"abort_reason,omitempty"`
	DistanceMeters int64      `json:"distance_meters"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (t Trip) Validate() error {
	if t.ID == "" || t.MissionID == "" || t.VehicleID == "" {
		return fmt.Errorf("trip identity, mission, and vehicle are required: %w", common.ErrInvalid)
	}
	if t.DistanceMeters < 0 {
		return common.FieldError{Field: "distance_meters", Problem: "must not be negative"}
	}
	return nil
}

func (t Trip) Start(now time.Time) (Trip, error) {
	if t.Status != StatusScheduled {
		return Trip{}, common.TransitionError{Entity: "trip", From: string(t.Status), To: string(StatusRunning), Reason: "only scheduled trips can start"}
	}
	if now.Before(t.ScheduledAt.Add(-5 * time.Minute)) {
		return Trip{}, common.ConflictError{Resource: "trip", Reason: "trip cannot start before its dispatch window"}
	}
	t.Status = StatusRunning
	started := now.UTC()
	t.StartedAt = &started
	t.Version++
	return t, nil
}

func (t Trip) Complete(now time.Time, distance int64) (Trip, error) {
	if t.Status != StatusRunning || t.StartedAt == nil {
		return Trip{}, common.TransitionError{Entity: "trip", From: string(t.Status), To: string(StatusCompleted), Reason: "trip is not running"}
	}
	if distance <= 0 {
		return Trip{}, common.FieldError{Field: "distance_meters", Problem: "must be positive"}
	}
	if now.Before(*t.StartedAt) {
		return Trip{}, common.FieldError{Field: "completed_at", Problem: "cannot precede start"}
	}
	t.Status = StatusCompleted
	t.DistanceMeters = distance
	completed := now.UTC()
	t.CompletedAt = &completed
	t.Version++
	return t, nil
}

func (t Trip) Abort(now time.Time, reason string) (Trip, error) {
	if t.Status != StatusScheduled && t.Status != StatusRunning {
		return Trip{}, common.TransitionError{Entity: "trip", From: string(t.Status), To: string(StatusAborted), Reason: "trip is already final"}
	}
	if len(reason) < 5 {
		return Trip{}, common.FieldError{Field: "reason", Problem: "must describe the operational cause"}
	}
	t.Status = StatusAborted
	t.AbortReason = reason
	completed := now.UTC()
	t.CompletedAt = &completed
	t.Version++
	return t, nil
}
