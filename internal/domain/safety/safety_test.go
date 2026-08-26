package safety

import (
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func validIncident(now time.Time) Incident {
	return Incident{ID: "i1", VehicleID: "v1", Severity: SeverityHigh, Category: "brake",
		Summary: "brake controller fault", Status: StatusOpen, Version: 1, OpenedAt: now, UpdatedAt: now}
}

func TestIncidentValidation(t *testing.T) {
	now := time.Now().UTC()
	base := validIncident(now)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid incident rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Incident)
	}{
		{"missing id", func(i *Incident) { i.ID = "" }},
		{"missing vehicle", func(i *Incident) { i.VehicleID = "" }},
		{"missing category", func(i *Incident) { i.Category = "" }},
		{"short summary", func(i *Incident) { i.Summary = "bad" }},
		{"invalid severity", func(i *Incident) { i.Severity = "unknown" }},
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

func TestSeverityValues(t *testing.T) {
	for _, severity := range []Severity{SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical} {
		if !severity.Valid() {
			t.Fatalf("valid severity rejected: %s", severity)
		}
	}
	for _, severity := range []Severity{"", "warning", "fatal"} {
		if severity.Valid() {
			t.Fatalf("invalid severity accepted: %s", severity)
		}
	}
}

func TestIncidentClaimLease(t *testing.T) {
	now := time.Now().UTC()
	base := validIncident(now)
	claimed, err := base.Claim("operator-a", now, 5*time.Minute)
	if err != nil {
		t.Fatalf("claim failed: %v", err)
	}
	if claimed.OwnerID != "operator-a" || claimed.Status != StatusAcknowledged || claimed.LeaseUntil == nil || claimed.Version != 2 {
		t.Fatalf("unexpected claim: %+v", claimed)
	}
	if _, err := claimed.Claim("operator-b", now.Add(time.Minute), 5*time.Minute); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("active lease should conflict: %v", err)
	}
	reclaimed, err := claimed.Claim("operator-b", now.Add(6*time.Minute), 5*time.Minute)
	if err != nil {
		t.Fatalf("expired lease was not reclaimable: %v", err)
	}
	if reclaimed.OwnerID != "operator-b" || reclaimed.Version != 3 {
		t.Fatalf("unexpected reclaimed incident: %+v", reclaimed)
	}
	if _, err := base.Claim("", now, time.Minute); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("empty owner should be invalid: %v", err)
	}
	if _, err := base.Claim("operator", now, 0); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("zero lease should be invalid: %v", err)
	}
}

func TestIncidentMitigationOwnership(t *testing.T) {
	now := time.Now().UTC()
	claimed, _ := validIncident(now).Claim("operator-a", now, time.Minute)
	mitigating, err := claimed.StartMitigation("operator-a", now)
	if err != nil {
		t.Fatalf("owner could not start mitigation: %v", err)
	}
	if mitigating.Status != StatusMitigating || mitigating.Version != claimed.Version+1 {
		t.Fatalf("unexpected mitigation state: %+v", mitigating)
	}
	if _, err := claimed.StartMitigation("operator-b", now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("wrong owner should conflict: %v", err)
	}
	if _, err := claimed.StartMitigation("operator-a", now.Add(time.Minute)); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("expired lease should conflict: %v", err)
	}
}

func TestIncidentResolutionAndClosure(t *testing.T) {
	now := time.Now().UTC()
	claimed, _ := validIncident(now).Claim("operator-a", now, time.Minute)
	mitigating, _ := claimed.StartMitigation("operator-a", now)
	if _, err := mitigating.Resolve("operator-a", "vehicle moved to safe bay", false, now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("unsafe vehicle should conflict: %v", err)
	}
	if _, err := mitigating.Resolve("operator-b", "vehicle moved to safe bay", true, now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("wrong owner should conflict: %v", err)
	}
	if _, err := mitigating.Resolve("operator-a", "short", true, now); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("short resolution should be invalid: %v", err)
	}
	resolved, err := mitigating.Resolve("operator-a", "vehicle moved to safe bay", true, now)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if resolved.Status != StatusResolved || resolved.LeaseUntil != nil || resolved.Resolution == "" {
		t.Fatalf("unexpected resolution: %+v", resolved)
	}
	if _, err := mitigating.Close(now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("unresolved close should conflict: %v", err)
	}
	closed, err := resolved.Close(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if closed.Status != StatusClosed || closed.ClosedAt == nil || closed.Version != resolved.Version+1 {
		t.Fatalf("unexpected closed incident: %+v", closed)
	}
}
