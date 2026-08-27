package worker

import (
	"context"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

type acknowledgedThenCancelledPublisher struct {
	cancel context.CancelFunc
	calls  int
}

func (p *acknowledgedThenCancelledPublisher) Publish(context.Context, string, []byte) error {
	p.calls++
	p.cancel()
	return nil
}

func TestPublishAcknowledgementWinsCancellationRace(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	repository := &fakeOutboxRepository{claimed: []job.Outbox{testEvent("ack-event")}}
	publisher := &acknowledgedThenCancelledPublisher{cancel: cancel}
	worker := NewOutbox(repository, publisher, clock.NewManual(now), workerLogger(), "worker-ack", time.Second, 10*time.Second)

	if err := worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("process acknowledged event: %v", err)
	}
	if publisher.calls != 1 {
		t.Fatalf("publish calls=%d, want one", publisher.calls)
	}
	if len(repository.completed) != 1 || repository.completed[0] != "ack-event" {
		t.Fatalf("completed=%v, acknowledged event must be completed", repository.completed)
	}
	if len(repository.failed) != 0 {
		t.Fatalf("failed=%v, acknowledged event must not be retried", repository.failed)
	}
}
