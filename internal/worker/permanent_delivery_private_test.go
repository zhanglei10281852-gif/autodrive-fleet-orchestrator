package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

type deliveryFailureRecord struct {
	id      string
	backoff time.Duration
	cause   error
}

type deliveryPolicyRepository struct {
	claimed   []job.Outbox
	completed []string
	failed    []deliveryFailureRecord
}

func (r *deliveryPolicyRepository) ClaimOutbox(_ context.Context, owner string, now time.Time, lease time.Duration, _ int) ([]job.Outbox, error) {
	claimed := append([]job.Outbox(nil), r.claimed...)
	for index := range claimed {
		claimed[index].Status = job.StatusLeased
		claimed[index].LeaseOwner = owner
		until := now.Add(lease)
		claimed[index].LeaseUntil = &until
		claimed[index].Attempt++
	}
	r.claimed = nil
	return claimed, nil
}

func (r *deliveryPolicyRepository) CompleteOutbox(_ context.Context, id, _ string, _ time.Time) error {
	r.completed = append(r.completed, id)
	return nil
}

func (r *deliveryPolicyRepository) FailOutbox(_ context.Context, id, _ string, _ time.Time, backoff time.Duration, cause error) error {
	r.failed = append(r.failed, deliveryFailureRecord{id: id, backoff: backoff, cause: cause})
	return nil
}

type deliveryPolicyPublisher struct {
	err error
}

func (p deliveryPolicyPublisher) Publish(context.Context, string, []byte) error {
	return p.err
}

func TestPermanentDeliveryFailureBypassesRetryBackoff(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	event := job.Outbox{
		ID: "evt-permanent-failure", Topic: "vehicle.command", AggregateType: "vehicle", AggregateID: "veh-1",
		Payload: []byte(`{"command":"retire"}`), Status: job.StatusPending, MaxAttempts: 5,
		AvailableAt: now, CreatedAt: now, UpdatedAt: now,
	}
	permanentCause := errors.New("destination rejected event schema")
	repository := &deliveryPolicyRepository{claimed: []job.Outbox{event}}
	publisher := deliveryPolicyPublisher{err: job.DeliveryError{Permanent: true, Cause: permanentCause}}
	outboxWorker := NewOutbox(repository, publisher, clock.NewManual(now), workerLogger(), "worker-policy", time.Second, 10*time.Second)
	if err := outboxWorker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process permanent failure: %v", err)
	}
	if len(repository.completed) != 0 || len(repository.failed) != 1 {
		t.Fatalf("permanent failure terminal calls completed=%v failed=%+v", repository.completed, repository.failed)
	}
	recorded := repository.failed[0]
	var classified job.DeliveryError
	if !errors.As(recorded.cause, &classified) || !classified.Permanent {
		t.Fatalf("permanent delivery identity was lost: %T %v", recorded.cause, recorded.cause)
	}
	if recorded.backoff != 0 {
		t.Fatalf("permanent failure received retry backoff: %v", recorded.backoff)
	}

	transientRepository := &deliveryPolicyRepository{claimed: []job.Outbox{event}}
	transientPublisher := deliveryPolicyPublisher{err: errors.New("broker unavailable")}
	transientWorker := NewOutbox(transientRepository, transientPublisher, clock.NewManual(now), workerLogger(), "worker-policy", time.Second, 10*time.Second)
	if err := transientWorker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process transient failure: %v", err)
	}
	if len(transientRepository.failed) != 1 || transientRepository.failed[0].backoff <= 0 {
		t.Fatalf("transient failure did not retain retry delay: %+v", transientRepository.failed)
	}
}
