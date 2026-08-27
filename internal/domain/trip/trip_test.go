package trip

import (
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func TestTripValidation(t *testing.T) {
	base := Trip{ID: "t1", MissionID: "m1", VehicleID: "v1", Status: StatusScheduled, DistanceMeters: 0}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid trip rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Trip)
	}{
		{"missing id", func(v *Trip) { v.ID = "" }},
		{"missing mission", func(v *Trip) { v.MissionID = "" }},
		{"missing vehicle", func(v *Trip) { v.VehicleID = "" }},
		{"negative distance", func(v *Trip) { v.DistanceMeters = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, common.ErrInvalid) {
				t.Fatalf("expected invalid error, got %v", err)
			}
		})
	}
}

func TestTripStartWindow(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	base := Trip{ID: "t1", MissionID: "m1", VehicleID: "v1", Status: StatusScheduled, ScheduledAt: now.Add(5 * time.Minute), Version: 1}
	started, err := base.Start(now)
	if err != nil {
		t.Fatalf("start at edge of dispatch window failed: %v", err)
	}
	if started.Status != StatusRunning || started.StartedAt == nil || !started.StartedAt.Equal(now) || started.Version != 2 {
		t.Fatalf("unexpected started trip: %+v", started)
	}
	tooEarly := base
	tooEarly.ScheduledAt = now.Add(5*time.Minute + time.Second)
	if _, err := tooEarly.Start(now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("early start should conflict: %v", err)
	}
	running := base
	running.Status = StatusRunning
	if _, err := running.Start(now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("running trip should not start again: %v", err)
	}
}

func TestTripCompletion(t *testing.T) {
	now := time.Now().UTC()
	started := now.Add(-time.Hour)
	base := Trip{ID: "t1", MissionID: "m1", VehicleID: "v1", Status: StatusRunning, StartedAt: &started, Version: 3}
	completed, err := base.Complete(now, 12500)
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if completed.Status != StatusCompleted || completed.CompletedAt == nil || completed.DistanceMeters != 12500 || completed.Version != 4 {
		t.Fatalf("unexpected completed trip: %+v", completed)
	}
	tests := []struct {
		name     string
		trip     Trip
		when     time.Time
		distance int64
	}{
		{"scheduled", Trip{Status: StatusScheduled}, now, 1},
		{"missing start", Trip{Status: StatusRunning}, now, 1},
		{"zero distance", base, now, 0},
		{"negative distance", base, now, -1},
		{"completion before start", base, started.Add(-time.Second), 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.trip.Complete(test.when, test.distance); err == nil {
				t.Fatal("expected completion failure")
			}
		})
	}
}

func TestTripAbort(t *testing.T) {
	now := time.Now().UTC()
	for _, status := range []Status{StatusScheduled, StatusRunning} {
		t.Run(string(status), func(t *testing.T) {
			value := Trip{Status: status, Version: 2}
			aborted, err := value.Abort(now, "safety operator requested stop")
			if err != nil {
				t.Fatalf("abort failed: %v", err)
			}
			if aborted.Status != StatusAborted || aborted.CompletedAt == nil || aborted.Version != 3 {
				t.Fatalf("unexpected abort: %+v", aborted)
			}
		})
	}
	if _, err := (Trip{Status: StatusCompleted}).Abort(now, "cannot abort completed"); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("final trip should conflict: %v", err)
	}
	if _, err := (Trip{Status: StatusScheduled}).Abort(now, "bad"); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("short reason should be invalid: %v", err)
	}
}
