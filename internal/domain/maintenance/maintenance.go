package maintenance

import (
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type Status string

const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusCompleted  Status = "completed"
	StatusCancelled  Status = "cancelled"
)

type WorkOrder struct {
	ID                    string     `json:"id"`
	VehicleID             string     `json:"vehicle_id"`
	Status                Status     `json:"status"`
	Reason                string     `json:"reason"`
	Priority              string     `json:"priority"`
	PreviousVehicleStatus string     `json:"previous_vehicle_status"`
	AssignedTechnician    string     `json:"assigned_technician,omitempty"`
	RequiredChecks        []string   `json:"required_checks"`
	CompletedChecks       []string   `json:"completed_checks"`
	Resolution            string     `json:"resolution,omitempty"`
	Version               int64      `json:"version"`
	CreatedBy             string     `json:"created_by"`
	CreatedAt             time.Time  `json:"created_at"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
}

func (w WorkOrder) Validate() error {
	if w.ID == "" || w.VehicleID == "" || w.CreatedBy == "" {
		return fmt.Errorf("work order identity and ownership are required: %w", common.ErrInvalid)
	}
	if len(strings.TrimSpace(w.Reason)) < 5 {
		return common.FieldError{Field: "reason", Problem: "must describe the maintenance need"}
	}
	if len(w.RequiredChecks) == 0 {
		return common.FieldError{Field: "required_checks", Problem: "must not be empty"}
	}
	seen := make(map[string]struct{}, len(w.RequiredChecks))
	for _, check := range w.RequiredChecks {
		name := strings.TrimSpace(check)
		if name == "" {
			return common.FieldError{Field: "required_checks", Problem: "contains an empty check"}
		}
		if _, exists := seen[name]; exists {
			return common.FieldError{Field: "required_checks", Problem: "contains duplicates"}
		}
		seen[name] = struct{}{}
	}
	return nil
}

func (w WorkOrder) Start(technician string, now time.Time) (WorkOrder, error) {
	if w.Status != StatusOpen {
		return WorkOrder{}, common.TransitionError{Entity: "maintenance", From: string(w.Status), To: string(StatusInProgress), Reason: "work order is not open"}
	}
	if strings.TrimSpace(technician) == "" {
		return WorkOrder{}, common.FieldError{Field: "technician", Problem: "is required"}
	}
	w.Status = StatusInProgress
	w.AssignedTechnician = technician
	started := now.UTC()
	w.StartedAt = &started
	w.Version++
	return w, nil
}

func (w WorkOrder) RecordCheck(check string) (WorkOrder, error) {
	if w.Status != StatusInProgress && w.Status != StatusBlocked {
		return WorkOrder{}, common.ConflictError{Resource: "work_order", Reason: "checks require active maintenance"}
	}
	if !contains(w.RequiredChecks, check) {
		return WorkOrder{}, common.FieldError{Field: "check", Problem: "is not required by this work order"}
	}
	if !contains(w.CompletedChecks, check) {
		w.CompletedChecks = append(append([]string(nil), w.CompletedChecks...), check)
		w.Version++
	}
	return w, nil
}

func (w WorkOrder) Complete(resolution string, now time.Time) (WorkOrder, error) {
	if w.Status != StatusInProgress {
		return WorkOrder{}, common.TransitionError{Entity: "maintenance", From: string(w.Status), To: string(StatusCompleted), Reason: "work order is not active"}
	}
	for _, required := range w.RequiredChecks {
		if !contains(w.CompletedChecks, required) {
			return WorkOrder{}, common.ConflictError{Resource: "work_order", Reason: "required checks remain incomplete"}
		}
	}
	if len(strings.TrimSpace(resolution)) < 10 {
		return WorkOrder{}, common.FieldError{Field: "resolution", Problem: "must describe completed work"}
	}
	w.Status = StatusCompleted
	w.Resolution = resolution
	completed := now.UTC()
	w.CompletedAt = &completed
	w.Version++
	return w, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
