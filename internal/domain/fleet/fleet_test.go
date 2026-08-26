package fleet

import (
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func TestRegionValidationAndCapacity(t *testing.T) {
	valid := Region{ID: "r1", Code: "PUDONG", Name: "Pudong", Timezone: "Asia/Shanghai", Status: RegionActive, MaxVehicles: 2}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid region rejected: %v", err)
	}
	if err := valid.CanAcceptVehicle(1); err != nil {
		t.Fatalf("capacity should remain: %v", err)
	}
	if err := valid.CanAcceptVehicle(2); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("expected capacity conflict, got %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Region)
	}{
		{"missing code", func(r *Region) { r.Code = "" }},
		{"missing name", func(r *Region) { r.Name = "" }},
		{"invalid timezone", func(r *Region) { r.Timezone = "Mars/Olympus" }},
		{"zero capacity", func(r *Region) { r.MaxVehicles = 0 }},
		{"negative capacity", func(r *Region) { r.MaxVehicles = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, common.ErrInvalid) {
				t.Fatalf("expected invalid error, got %v", err)
			}
		})
	}
}

func TestRegionStateMachine(t *testing.T) {
	tests := []struct {
		from    RegionStatus
		to      RegionStatus
		allowed bool
	}{
		{RegionDraft, RegionActive, true},
		{RegionDraft, RegionRetired, true},
		{RegionDraft, RegionPaused, false},
		{RegionActive, RegionPaused, true},
		{RegionActive, RegionRetired, true},
		{RegionActive, RegionDraft, false},
		{RegionPaused, RegionActive, true},
		{RegionPaused, RegionRetired, true},
		{RegionPaused, RegionDraft, false},
		{RegionRetired, RegionActive, false},
		{RegionRetired, RegionPaused, false},
	}
	for _, test := range tests {
		t.Run(string(test.from)+"_to_"+string(test.to), func(t *testing.T) {
			region := Region{Status: test.from, Version: 7}
			updated, err := region.Transition(test.to)
			if test.allowed {
				if err != nil {
					t.Fatalf("transition rejected: %v", err)
				}
				if updated.Status != test.to || updated.Version != 8 {
					t.Fatalf("unexpected transition result: %+v", updated)
				}
			} else if !errors.Is(err, common.ErrConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
		})
	}
}

func TestVehicleValidation(t *testing.T) {
	valid := Vehicle{ID: "v1", RegionID: "r1", VIN: "VIN00000001", FleetNumber: "AV-001", Status: VehicleOffline, Capability: "passenger", BatteryPercent: 50}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid vehicle rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Vehicle)
	}{
		{"missing id", func(v *Vehicle) { v.ID = "" }},
		{"missing region", func(v *Vehicle) { v.RegionID = "" }},
		{"short vin", func(v *Vehicle) { v.VIN = "123" }},
		{"missing fleet number", func(v *Vehicle) { v.FleetNumber = "" }},
		{"negative battery", func(v *Vehicle) { v.BatteryPercent = -1 }},
		{"excess battery", func(v *Vehicle) { v.BatteryPercent = 101 }},
		{"bad latitude", func(v *Vehicle) { v.Latitude = 91 }},
		{"bad longitude", func(v *Vehicle) { v.Longitude = -181 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, common.ErrInvalid) {
				t.Fatalf("expected invalid error, got %v", err)
			}
		})
	}
}

func TestVehicleDispatchEligibility(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	validUntil := now.Add(time.Hour)
	base := Vehicle{Status: VehicleAvailable, BatteryPercent: 80, SafetyValidUntil: &validUntil}
	if err := base.CanDispatch(now, 30); err != nil {
		t.Fatalf("eligible vehicle rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Vehicle)
	}{
		{"offline", func(v *Vehicle) { v.Status = VehicleOffline }},
		{"reserved", func(v *Vehicle) { v.Status = VehicleReserved }},
		{"low battery", func(v *Vehicle) { v.BatteryPercent = 29 }},
		{"missing inspection", func(v *Vehicle) { v.SafetyValidUntil = nil }},
		{"expired inspection", func(v *Vehicle) { expired := now; v.SafetyValidUntil = &expired }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if err := candidate.CanDispatch(now, 30); !errors.Is(err, common.ErrConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
		})
	}
}

func TestVehicleStateMachine(t *testing.T) {
	allowed := map[VehicleStatus][]VehicleStatus{
		VehicleDraft:       {VehicleOffline},
		VehicleOffline:     {VehicleAvailable, VehicleMaintenance, VehicleSuspended},
		VehicleAvailable:   {VehicleOffline, VehicleReserved, VehicleCharging, VehicleMaintenance, VehicleSuspended},
		VehicleReserved:    {VehicleAvailable, VehicleInTrip, VehicleSuspended},
		VehicleInTrip:      {VehicleAvailable, VehicleCharging, VehicleMaintenance, VehicleSuspended},
		VehicleCharging:    {VehicleAvailable, VehicleOffline, VehicleMaintenance},
		VehicleMaintenance: {VehicleOffline, VehicleAvailable, VehicleSuspended},
		VehicleSuspended:   {VehicleOffline, VehicleMaintenance},
	}
	all := []VehicleStatus{VehicleDraft, VehicleOffline, VehicleAvailable, VehicleReserved, VehicleInTrip, VehicleCharging, VehicleMaintenance, VehicleSuspended}
	for _, from := range all {
		for _, to := range all {
			t.Run(string(from)+"_"+string(to), func(t *testing.T) {
				vehicle := Vehicle{Status: from, Version: 10}
				updated, err := vehicle.Transition(to)
				expected := containsStatus(allowed[from], to)
				if expected && err != nil {
					t.Fatalf("allowed transition rejected: %v", err)
				}
				if !expected && !errors.Is(err, common.ErrConflict) {
					t.Fatalf("forbidden transition did not conflict: %v", err)
				}
				if expected && (updated.Status != to || updated.Version != 11) {
					t.Fatalf("unexpected result: %+v", updated)
				}
			})
		}
	}
}

func containsStatus(values []VehicleStatus, target VehicleStatus) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
