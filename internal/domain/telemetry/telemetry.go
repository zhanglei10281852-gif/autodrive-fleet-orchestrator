package telemetry

import (
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Sample struct {
	EventID        string    `json:"event_id"`
	VehicleID      string    `json:"vehicle_id"`
	ObservedAt     time.Time `json:"observed_at"`
	Latitude       float64   `json:"latitude"`
	Longitude      float64   `json:"longitude"`
	SpeedKPH       float64   `json:"speed_kph"`
	BatteryPercent int       `json:"battery_percent"`
	OdometerMeters int64     `json:"odometer_meters"`
	FaultCode      string    `json:"fault_code,omitempty"`
	Severity       Severity  `json:"severity"`
	PayloadHash    string    `json:"payload_hash"`
	ReceivedAt     time.Time `json:"received_at"`
}

func (s Sample) Validate(now time.Time) error {
	if strings.TrimSpace(s.EventID) == "" || strings.TrimSpace(s.VehicleID) == "" {
		return fmt.Errorf("telemetry event and vehicle identity are required: %w", common.ErrInvalid)
	}
	if s.ObservedAt.IsZero() || s.ObservedAt.After(now.Add(5*time.Minute)) {
		return common.FieldError{Field: "observed_at", Problem: "is outside the accepted clock-skew window"}
	}
	if s.Latitude < -90 || s.Latitude > 90 || s.Longitude < -180 || s.Longitude > 180 {
		return common.FieldError{Field: "location", Problem: "coordinates are outside valid range"}
	}
	if s.SpeedKPH < 0 || s.SpeedKPH > 300 {
		return common.FieldError{Field: "speed_kph", Problem: "must be between 0 and 300"}
	}
	if s.BatteryPercent < 0 || s.BatteryPercent > 100 {
		return common.FieldError{Field: "battery_percent", Problem: "must be between 0 and 100"}
	}
	if s.OdometerMeters < 0 {
		return common.FieldError{Field: "odometer_meters", Problem: "must not be negative"}
	}
	if !s.Severity.Valid() {
		return common.FieldError{Field: "severity", Problem: "is not supported"}
	}
	return nil
}

func (s Severity) Valid() bool {
	return s == SeverityInfo || s == SeverityWarning || s == SeverityCritical
}

func (s Sample) RaisesIncident() bool {
	return s.Severity == SeverityCritical || (s.Severity == SeverityWarning && s.FaultCode != "")
}

type IngestResult struct {
	EventID   string `json:"event_id"`
	Accepted  bool   `json:"accepted"`
	Duplicate bool   `json:"duplicate"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

type BatchResult struct {
	Items    []IngestResult `json:"items"`
	Accepted int            `json:"accepted"`
	Rejected int            `json:"rejected"`
}

func NewBatchResult(capacity int) BatchResult {
	return BatchResult{Items: make([]IngestResult, 0, capacity)}
}

func (b *BatchResult) Add(result IngestResult) {
	b.Items = append(b.Items, result)
	if result.Accepted {
		b.Accepted++
	} else {
		b.Rejected++
	}
}
