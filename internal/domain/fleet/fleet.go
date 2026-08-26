package fleet

import (
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

type VehicleStatus string

const (
	VehicleDraft       VehicleStatus = "draft"
	VehicleOffline     VehicleStatus = "offline"
	VehicleAvailable   VehicleStatus = "available"
	VehicleReserved    VehicleStatus = "reserved"
	VehicleInTrip      VehicleStatus = "in_trip"
	VehicleCharging    VehicleStatus = "charging"
	VehicleMaintenance VehicleStatus = "maintenance"
	VehicleSuspended   VehicleStatus = "suspended"
)

type RegionStatus string

const (
	RegionDraft   RegionStatus = "draft"
	RegionActive  RegionStatus = "active"
	RegionPaused  RegionStatus = "paused"
	RegionRetired RegionStatus = "retired"
)

type Region struct {
	ID          string       `json:"id"`
	Code        string       `json:"code"`
	Name        string       `json:"name"`
	Timezone    string       `json:"timezone"`
	Status      RegionStatus `json:"status"`
	MaxVehicles int          `json:"max_vehicles"`
	Version     int64        `json:"version"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

func (r Region) Validate() error {
	if strings.TrimSpace(r.Code) == "" {
		return common.FieldError{Field: "code", Problem: "is required"}
	}
	if strings.TrimSpace(r.Name) == "" {
		return common.FieldError{Field: "name", Problem: "is required"}
	}
	if _, err := time.LoadLocation(r.Timezone); err != nil {
		return common.FieldError{Field: "timezone", Problem: "must be an IANA timezone"}
	}
	if r.MaxVehicles <= 0 {
		return common.FieldError{Field: "max_vehicles", Problem: "must be positive"}
	}
	return nil
}

func (r Region) CanAcceptVehicle(current int) error {
	if r.Status != RegionActive {
		return common.ConflictError{Resource: "region", Reason: "region is not active"}
	}
	if current >= r.MaxVehicles {
		return common.ConflictError{Resource: "region", Reason: "vehicle capacity is exhausted"}
	}
	return nil
}

func (r Region) Transition(to RegionStatus) (Region, error) {
	allowed := map[RegionStatus]map[RegionStatus]bool{
		RegionDraft:   {RegionActive: true, RegionRetired: true},
		RegionActive:  {RegionPaused: true, RegionRetired: true},
		RegionPaused:  {RegionActive: true, RegionRetired: true},
		RegionRetired: {},
	}
	if !allowed[r.Status][to] {
		return Region{}, common.TransitionError{Entity: "region", From: string(r.Status), To: string(to), Reason: "transition is not permitted"}
	}
	r.Status = to
	r.Version++
	return r, nil
}

type Vehicle struct {
	ID               string        `json:"id"`
	RegionID         string        `json:"region_id"`
	VIN              string        `json:"vin"`
	FleetNumber      string        `json:"fleet_number"`
	Status           VehicleStatus `json:"status"`
	Capability       string        `json:"capability"`
	BatteryPercent   int           `json:"battery_percent"`
	Latitude         float64       `json:"latitude"`
	Longitude        float64       `json:"longitude"`
	LastTelemetryAt  *time.Time    `json:"last_telemetry_at,omitempty"`
	SafetyValidUntil *time.Time    `json:"safety_valid_until,omitempty"`
	Version          int64         `json:"version"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

func (v Vehicle) Validate() error {
	if v.ID == "" || v.RegionID == "" {
		return fmt.Errorf("vehicle identity and region are required: %w", common.ErrInvalid)
	}
	if len(strings.TrimSpace(v.VIN)) < 8 {
		return common.FieldError{Field: "vin", Problem: "must contain at least 8 characters"}
	}
	if strings.TrimSpace(v.FleetNumber) == "" {
		return common.FieldError{Field: "fleet_number", Problem: "is required"}
	}
	if v.BatteryPercent < 0 || v.BatteryPercent > 100 {
		return common.FieldError{Field: "battery_percent", Problem: "must be between 0 and 100"}
	}
	if v.Latitude < -90 || v.Latitude > 90 || v.Longitude < -180 || v.Longitude > 180 {
		return common.FieldError{Field: "location", Problem: "coordinates are outside valid range"}
	}
	return nil
}

func (v Vehicle) CanDispatch(now time.Time, minimumBattery int) error {
	if v.Status != VehicleAvailable {
		return common.ConflictError{Resource: "vehicle", Reason: "vehicle is not available"}
	}
	if v.BatteryPercent < minimumBattery {
		return common.ConflictError{Resource: "vehicle", Reason: "battery is below mission minimum"}
	}
	if v.SafetyValidUntil == nil || !v.SafetyValidUntil.After(now) {
		return common.ConflictError{Resource: "vehicle", Reason: "safety inspection has expired"}
	}
	return nil
}

func (v Vehicle) Transition(to VehicleStatus) (Vehicle, error) {
	allowed := map[VehicleStatus]map[VehicleStatus]bool{
		VehicleDraft:       {VehicleOffline: true},
		VehicleOffline:     {VehicleAvailable: true, VehicleMaintenance: true, VehicleSuspended: true},
		VehicleAvailable:   {VehicleOffline: true, VehicleReserved: true, VehicleCharging: true, VehicleMaintenance: true, VehicleSuspended: true},
		VehicleReserved:    {VehicleAvailable: true, VehicleInTrip: true, VehicleSuspended: true},
		VehicleInTrip:      {VehicleAvailable: true, VehicleCharging: true, VehicleMaintenance: true, VehicleSuspended: true},
		VehicleCharging:    {VehicleAvailable: true, VehicleOffline: true, VehicleMaintenance: true},
		VehicleMaintenance: {VehicleOffline: true, VehicleAvailable: true, VehicleSuspended: true},
		VehicleSuspended:   {VehicleOffline: true, VehicleMaintenance: true},
	}
	if !allowed[v.Status][to] {
		return Vehicle{}, common.TransitionError{Entity: "vehicle", From: string(v.Status), To: string(to), Reason: "transition is not permitted"}
	}
	v.Status = to
	v.Version++
	return v, nil
}

type Filter struct {
	RegionID   string
	Status     VehicleStatus
	Capability string
	MinBattery int
	Search     string
	Page       common.PageRequest
}
