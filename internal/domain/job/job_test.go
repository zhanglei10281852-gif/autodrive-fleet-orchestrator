package job

import (
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func validJob(now time.Time) Outbox {
	return Outbox{ID: "e1", Topic: "trip.completed", AggregateType: "trip", AggregateID: "t1",
		Payload: []byte(`{"trip_id":"t1"}`), Status: StatusPending, MaxAttempts: 3,
		AvailableAt: now, CreatedAt: now, UpdatedAt: now}
}

func TestOutboxValidation(t *testing.T) {
	now := time.Now().UTC()
	base := validJob(now)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid outbox rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Outbox)
	}{
		{"missing id", func(o *Outbox) { o.ID = "" }},
		{"missing topic", func(o *Outbox) { o.Topic = "" }},
		{"missing aggregate", func(o *Outbox) { o.AggregateID = "" }},
		{"invalid json", func(o *Outbox) { o.Payload = []byte("{") }},
		{"zero attempts", func(o *Outbox) { o.MaxAttempts = 0 }},
		{"too many attempts", func(o *Outbox) { o.MaxAttempts = 21 }},
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

func TestOutboxLeaseEligibility(t *testing.T) {
	now := time.Now().UTC()
	pending := validJob(now)
	if !pending.CanLease(now) {
		t.Fatal("available pending job should lease")
	}
	future := pending
	future.AvailableAt = now.Add(time.Minute)
	if future.CanLease(now) {
		t.Fatal("future job should not lease")
	}
	leaseUntil := now.Add(time.Minute)
	leased := pending
	leased.Status = StatusLeased
	leased.LeaseUntil = &leaseUntil
	if leased.CanLease(now) {
		t.Fatal("active lease should not be claimable")
	}
	expired := now.Add(-time.Second)
	leased.LeaseUntil = &expired
	if !leased.CanLease(now) {
		t.Fatal("expired lease should be reclaimable")
	}
	completed := pending
	completed.Status = StatusCompleted
	if completed.CanLease(now) {
		t.Fatal("completed job should not lease")
	}
}

func TestOutboxLeaseComplete(t *testing.T) {
	now := time.Now().UTC()
	base := validJob(now)
	leased, err := base.Lease("worker-a", now, time.Minute)
	if err != nil {
		t.Fatalf("lease failed: %v", err)
	}
	if leased.Status != StatusLeased || leased.LeaseOwner != "worker-a" || leased.LeaseUntil == nil || leased.Attempt != 1 {
		t.Fatalf("unexpected lease: %+v", leased)
	}
	if _, err := base.Lease("", now, time.Minute); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("empty owner should be invalid: %v", err)
	}
	if _, err := leased.Lease("worker-b", now, time.Minute); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("active lease should conflict: %v", err)
	}
	if _, err := leased.Complete("worker-b", now); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("wrong owner should conflict: %v", err)
	}
	completed, err := leased.Complete("worker-a", now)
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if completed.Status != StatusCompleted || completed.LeaseOwner != "" || completed.LeaseUntil != nil {
		t.Fatalf("unexpected completion: %+v", completed)
	}
}

func TestOutboxRetryAndDeadLetter(t *testing.T) {
	now := time.Now().UTC()
	base := validJob(now)
	first, _ := base.Lease("worker", now, time.Minute)
	failed, err := first.Fail("worker", errors.New("temporary"), now, 2*time.Second)
	if err != nil {
		t.Fatalf("fail failed: %v", err)
	}
	if failed.Status != StatusPending || failed.LastError != "temporary" || !failed.AvailableAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("unexpected retry: %+v", failed)
	}
	failed.Attempt = failed.MaxAttempts - 1
	failed.AvailableAt = now
	last, _ := failed.Lease("worker", now, time.Minute)
	dead, err := last.Fail("worker", errors.New("still broken"), now, time.Second)
	if err != nil {
		t.Fatalf("dead letter failed: %v", err)
	}
	if dead.Status != StatusDead || dead.LastError != "still broken" {
		t.Fatalf("unexpected dead letter: %+v", dead)
	}
	if _, err := first.Fail("wrong", errors.New("x"), now, time.Second); !errors.Is(err, common.ErrConflict) {
		t.Fatalf("wrong owner should conflict: %v", err)
	}
	if _, err := first.Fail("worker", nil, now, time.Second); !errors.Is(err, common.ErrInvalid) {
		t.Fatalf("missing cause should be invalid: %v", err)
	}
}
