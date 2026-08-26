package mission

import (
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func validMission(now time.Time) Mission {
	return Mission{ID: "m1", RegionID: "r1", ExternalReference: "ext", IdempotencyKey: "key",
		Kind: "passenger", Priority: PriorityRoutine, Status: StatusPending, PickupLatitude: 31.2,
		PickupLongitude: 121.4, DropoffLatitude: 31.3, DropoffLongitude: 121.5,
		EarliestStartAt: now.Add(time.Minute), DeadlineAt: now.Add(time.Hour), MinimumBattery: 30,
		RequiredCapability: "passenger", Version: 1, CreatedBy: "u1", CreatedAt: now, UpdatedAt: now}
}

func TestMissionValidation(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	base := validMission(now)
	if err := base.Validate(now); err != nil {
		t.Fatalf("valid mission rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Mission)
	}{
		{"missing id", func(m *Mission) { m.ID = "" }},
		{"missing region", func(m *Mission) { m.RegionID = "" }},
		{"missing creator", func(m *Mission) { m.CreatedBy = "" }},
		{"missing idempotency", func(m *Mission) { m.IdempotencyKey = "" }},
		{"missing kind", func(m *Mission) { m.Kind = "" }},
		{"missing capability", func(m *Mission) { m.RequiredCapability = "" }},
		{"low minimum battery", func(m *Mission) { m.MinimumBattery = 4 }},
		{"high minimum battery", func(m *Mission) { m.MinimumBattery = 101 }},
		{"deadline before start", func(m *Mission) { m.DeadlineAt = m.EarliestStartAt }},
		{"deadline in past", func(m *Mission) { m.DeadlineAt = now.Add(-time.Minute) }},
		{"bad pickup latitude", func(m *Mission) { m.PickupLatitude = 100 }},
		{"bad pickup longitude", func(m *Mission) { m.PickupLongitude = 200 }},
		{"bad dropoff latitude", func(m *Mission) { m.DropoffLatitude = -100 }},
		{"bad dropoff longitude", func(m *Mission) { m.DropoffLongitude = -200 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if err := candidate.Validate(now); !errors.Is(err, common.ErrInvalid) {
				t.Fatalf("expected invalid, got %v", err)
			}
		})
	}
}

func TestMissionAssignmentAndRelease(t *testing.T) {
	now := time.Now().UTC()
	base := validMission(now)
	assigned, err := base.Assign("v1")
	if err != nil {
		t.Fatalf("assign failed: %v", err)
	}
	if assigned.Status != StatusAssigned || assigned.AssignedVehicleID != "v1" || assigned.Version != 2 {
		t.Fatalf("unexpected assignment: %+v", assigned)
	}
	released, err := assigned.Transition(StatusPending)
	if err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if released.AssignedVehicleID != "" || released.Status != StatusPending || released.Version != 3 {
		t.Fatalf("unexpected release: %+v", released)
	}
	if _, err := base.Assign(""); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("empty vehicle should conflict: %v", err)
	}
	completed := base
	completed.Status = StatusCompleted
	if _, err := completed.Assign("v1"); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("completed mission should not assign: %v", err)
	}
}

func TestMissionCanAssign(t *testing.T) {
	now := time.Now().UTC()
	base := validMission(now)
	if err := base.CanAssign(now); err != nil {
		t.Fatalf("pending mission rejected: %v", err)
	}
	assigned := base
	assigned.Status = StatusAssigned
	if err := assigned.CanAssign(now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("assigned mission should conflict: %v", err)
	}
	expired := base
	expired.DeadlineAt = now.Add(-time.Second)
	if err := expired.CanAssign(now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("expired mission should conflict: %v", err)
	}
}

func TestMissionStateMachine(t *testing.T) {
	allowed := map[Status][]Status{
		StatusPending:    {StatusAssigned, StatusCancelled},
		StatusAssigned:   {StatusInProgress, StatusPending, StatusCancelled, StatusFailed},
		StatusInProgress: {StatusCompleted, StatusFailed, StatusCancelled},
		StatusCompleted:  {},
		StatusCancelled:  {},
		StatusFailed:     {},
	}
	all := []Status{StatusPending, StatusAssigned, StatusInProgress, StatusCompleted, StatusCancelled, StatusFailed}
	for _, from := range all {
		for _, to := range all {
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				value := Mission{Status: from, AssignedVehicleID: "v1", Version: 4}
				updated, err := value.Transition(to)
				expected := statusIncluded(allowed[from], to)
				if expected && err != nil {
					t.Fatalf("allowed transition rejected: %v", err)
				}
				if !expected && !errors.Is(err, common.ErrConflict) {
					t.Fatalf("forbidden transition did not conflict: %v", err)
				}
				if expected && updated.Version != 5 {
					t.Fatalf("version not advanced: %+v", updated)
				}
				if expected && to == StatusPending && updated.AssignedVehicleID != "" {
					t.Fatalf("released mission kept vehicle: %+v", updated)
				}
			})
		}
	}
}

func statusIncluded(values []Status, target Status) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
