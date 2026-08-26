package maintenance

import (
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func validOrder(now time.Time) WorkOrder {
	return WorkOrder{ID: "w1", VehicleID: "v1", Status: StatusOpen, Reason: "scheduled brake inspection",
		Priority: "normal", PreviousVehicleStatus: "available", RequiredChecks: []string{"brake", "steering"},
		CompletedChecks: []string{}, Version: 1, CreatedBy: "u1", CreatedAt: now}
}

func TestWorkOrderValidation(t *testing.T) {
	now := time.Now().UTC()
	base := validOrder(now)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid order rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*WorkOrder)
	}{
		{"missing id", func(w *WorkOrder) { w.ID = "" }},
		{"missing vehicle", func(w *WorkOrder) { w.VehicleID = "" }},
		{"missing creator", func(w *WorkOrder) { w.CreatedBy = "" }},
		{"short reason", func(w *WorkOrder) { w.Reason = "bad" }},
		{"no checks", func(w *WorkOrder) { w.RequiredChecks = nil }},
		{"empty check", func(w *WorkOrder) { w.RequiredChecks = []string{"brake", ""} }},
		{"duplicate check", func(w *WorkOrder) { w.RequiredChecks = []string{"brake", "brake"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, common.ErrInvalid) {
				t.Fatalf("expected invalid, got %v", err)
			}
		})
	}
}

func TestWorkOrderStart(t *testing.T) {
	now := time.Now().UTC()
	base := validOrder(now)
	started, err := base.Start("technician-a", now)
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if started.Status != StatusInProgress || started.AssignedTechnician != "technician-a" || started.StartedAt == nil || started.Version != 2 {
		t.Fatalf("unexpected started order: %+v", started)
	}
	if _, err := base.Start("", now); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("empty technician should be invalid: %v", err)
	}
	completed := base
	completed.Status = StatusCompleted
	if _, err := completed.Start("technician", now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("completed order should conflict: %v", err)
	}
}

func TestRecordCheckIsIdempotent(t *testing.T) {
	now := time.Now().UTC()
	started, _ := validOrder(now).Start("technician", now)
	first, err := started.RecordCheck("brake")
	if err != nil {
		t.Fatalf("record check failed: %v", err)
	}
	if len(first.CompletedChecks) != 1 || first.Version != started.Version+1 {
		t.Fatalf("unexpected first check: %+v", first)
	}
	second, err := first.RecordCheck("brake")
	if err != nil {
		t.Fatalf("repeat check failed: %v", err)
	}
	if len(second.CompletedChecks) != 1 || second.Version != first.Version {
		t.Fatalf("repeat check should be idempotent: %+v", second)
	}
	if _, err := started.RecordCheck("battery"); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("unknown check should be invalid: %v", err)
	}
	if _, err := validOrder(now).RecordCheck("brake"); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("open order should reject check: %v", err)
	}
}

func TestCompleteRequiresAllChecks(t *testing.T) {
	now := time.Now().UTC()
	started, _ := validOrder(now).Start("technician", now)
	partial, _ := started.RecordCheck("brake")
	if _, err := partial.Complete("brakes adjusted and verified", now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("partial completion should conflict: %v", err)
	}
	ready, _ := partial.RecordCheck("steering")
	if _, err := ready.Complete("short", now); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("short resolution should be invalid: %v", err)
	}
	completed, err := ready.Complete("brakes and steering verified", now)
	if err != nil {
		t.Fatalf("completion failed: %v", err)
	}
	if completed.Status != StatusCompleted || completed.CompletedAt == nil || completed.Version != ready.Version+1 {
		t.Fatalf("unexpected completion: %+v", completed)
	}
}
