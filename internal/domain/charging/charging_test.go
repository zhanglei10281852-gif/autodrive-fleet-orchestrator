package charging

import (
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func TestConnectorValidation(t *testing.T) {
	base := Connector{ID: "c1", StationID: "s1", Code: "A", PowerKW: 120}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid connector rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Connector)
	}{
		{"missing id", func(c *Connector) { c.ID = "" }},
		{"missing station", func(c *Connector) { c.StationID = "" }},
		{"missing code", func(c *Connector) { c.Code = "" }},
		{"too weak", func(c *Connector) { c.PowerKW = 6 }},
		{"too strong", func(c *Connector) { c.PowerKW = 1001 }},
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

func validSession(now time.Time) Session {
	return Session{ID: "chg1", VehicleID: "v1", ConnectorID: "c1", Status: StatusReserved,
		WindowStart: now.Add(time.Hour), WindowEnd: now.Add(2 * time.Hour), InitialBattery: 20,
		IdempotencyKey: "key", Version: 1, CreatedBy: "u1", CreatedAt: now}
}

func TestSessionValidation(t *testing.T) {
	now := time.Now().UTC()
	base := validSession(now)
	if err := base.Validate(now); err != nil {
		t.Fatalf("valid session rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Session)
	}{
		{"missing id", func(s *Session) { s.ID = "" }},
		{"missing vehicle", func(s *Session) { s.VehicleID = "" }},
		{"missing connector", func(s *Session) { s.ConnectorID = "" }},
		{"missing creator", func(s *Session) { s.CreatedBy = "" }},
		{"window reversed", func(s *Session) { s.WindowEnd = s.WindowStart }},
		{"window expired", func(s *Session) { s.WindowEnd = now.Add(-time.Minute) }},
		{"window too long", func(s *Session) { s.WindowEnd = s.WindowStart.Add(9 * time.Hour) }},
		{"negative battery", func(s *Session) { s.InitialBattery = -1 }},
		{"excess battery", func(s *Session) { s.InitialBattery = 101 }},
		{"missing key", func(s *Session) { s.IdempotencyKey = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			test.mutate(&candidate)
			if err := candidate.Validate(now); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestSessionOverlap(t *testing.T) {
	now := time.Now().UTC()
	session := validSession(now)
	tests := []struct {
		name     string
		start    time.Time
		end      time.Time
		overlaps bool
	}{
		{"inside", session.WindowStart.Add(time.Minute), session.WindowEnd.Add(-time.Minute), true},
		{"covers", session.WindowStart.Add(-time.Hour), session.WindowEnd.Add(time.Hour), true},
		{"touch start", session.WindowStart.Add(-time.Hour), session.WindowStart, false},
		{"touch end", session.WindowEnd, session.WindowEnd.Add(time.Hour), false},
		{"before", session.WindowStart.Add(-2 * time.Hour), session.WindowStart.Add(-time.Hour), false},
		{"after", session.WindowEnd.Add(time.Hour), session.WindowEnd.Add(2 * time.Hour), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := session.Overlaps(test.start, test.end); got != test.overlaps {
				t.Fatalf("overlap=%v want %v", got, test.overlaps)
			}
		})
	}
}

func TestSessionStartAndComplete(t *testing.T) {
	now := time.Now().UTC()
	base := validSession(now)
	started, err := base.Start(base.WindowStart.Add(-10 * time.Minute))
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if started.Status != StatusActive || started.StartedAt == nil || started.Version != 2 {
		t.Fatalf("unexpected start: %+v", started)
	}
	completed, err := started.Complete(base.WindowStart.Add(time.Hour), 90, 50000)
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if completed.Status != StatusCompleted || completed.FinalBattery == nil || *completed.FinalBattery != 90 || completed.Version != 3 {
		t.Fatalf("unexpected completion: %+v", completed)
	}
	if _, err := base.Start(base.WindowStart.Add(-11 * time.Minute)); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("early start should conflict: %v", err)
	}
	if _, err := base.Start(base.WindowEnd); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("late start should conflict: %v", err)
	}
	if _, err := started.Complete(now, 10, 1); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("battery regression should be invalid: %v", err)
	}
	if _, err := started.Complete(now, 101, 1); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("excess battery should be invalid: %v", err)
	}
	if _, err := started.Complete(now, 50, 0); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("zero energy should be invalid: %v", err)
	}
}
