package charging

import (
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type Status string

const (
	StatusReserved  Status = "reserved"
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
)

type Station struct {
	ID        string    `json:"id"`
	RegionID  string    `json:"region_id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Connector struct {
	ID           string     `json:"id"`
	StationID    string     `json:"station_id"`
	Code         string     `json:"code"`
	PowerKW      int        `json:"power_kw"`
	Active       bool       `json:"active"`
	Version      int64      `json:"version"`
	LeaseOwnerID string     `json:"lease_owner_id,omitempty"`
	LeaseUntil   *time.Time `json:"lease_until,omitempty"`
}

func (c Connector) Validate() error {
	if c.ID == "" || c.StationID == "" || strings.TrimSpace(c.Code) == "" {
		return fmt.Errorf("connector identity, station, and code are required: %w", common.ErrInvalid)
	}
	if c.PowerKW < 7 || c.PowerKW > 1000 {
		return common.FieldError{Field: "power_kw", Problem: "must be between 7 and 1000"}
	}
	return nil
}

type Session struct {
	ID              string     `json:"id"`
	VehicleID       string     `json:"vehicle_id"`
	ConnectorID     string     `json:"connector_id"`
	Status          Status     `json:"status"`
	WindowStart     time.Time  `json:"window_start"`
	WindowEnd       time.Time  `json:"window_end"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	InitialBattery  int        `json:"initial_battery"`
	FinalBattery    *int       `json:"final_battery,omitempty"`
	EnergyWattHours int64      `json:"energy_watt_hours"`
	IdempotencyKey  string     `json:"idempotency_key"`
	Version         int64      `json:"version"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
}

func (s Session) Validate(now time.Time) error {
	if s.ID == "" || s.VehicleID == "" || s.ConnectorID == "" || s.CreatedBy == "" {
		return fmt.Errorf("charging session identity and ownership are required: %w", common.ErrInvalid)
	}
	if !s.WindowEnd.After(s.WindowStart) || !s.WindowEnd.After(now) {
		return common.FieldError{Field: "window", Problem: "must end after its start and in the future"}
	}
	if s.WindowEnd.Sub(s.WindowStart) > 8*time.Hour {
		return common.FieldError{Field: "window", Problem: "cannot exceed eight hours"}
	}
	if s.InitialBattery < 0 || s.InitialBattery > 100 {
		return common.FieldError{Field: "initial_battery", Problem: "must be between 0 and 100"}
	}
	if strings.TrimSpace(s.IdempotencyKey) == "" {
		return common.FieldError{Field: "idempotency_key", Problem: "is required"}
	}
	return nil
}

func (s Session) Overlaps(start, end time.Time) bool {
	return s.WindowStart.Before(end) && start.Before(s.WindowEnd)
}

func (s Session) Start(now time.Time) (Session, error) {
	if s.Status != StatusReserved {
		return Session{}, common.TransitionError{Entity: "charging_session", From: string(s.Status), To: string(StatusActive), Reason: "reservation is not active"}
	}
	if now.Before(s.WindowStart.Add(-10*time.Minute)) || !now.Before(s.WindowEnd) {
		return Session{}, common.ConflictError{Resource: "charging_session", Reason: "outside reservation window"}
	}
	s.Status = StatusActive
	started := now.UTC()
	s.StartedAt = &started
	s.Version++
	return s, nil
}

func (s Session) Complete(now time.Time, battery int, energy int64) (Session, error) {
	if s.Status != StatusActive || s.StartedAt == nil {
		return Session{}, common.TransitionError{Entity: "charging_session", From: string(s.Status), To: string(StatusCompleted), Reason: "session is not active"}
	}
	if battery < s.InitialBattery || battery > 100 || energy <= 0 {
		return Session{}, common.FieldError{Field: "completion", Problem: "battery and energy are inconsistent"}
	}
	s.Status = StatusCompleted
	s.FinalBattery = &battery
	s.EnergyWattHours = energy
	completed := now.UTC()
	s.CompletedAt = &completed
	s.Version++
	return s, nil
}
