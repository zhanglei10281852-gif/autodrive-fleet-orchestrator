package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

type failFirstPublisher struct {
	mu     sync.Mutex
	topics []string
}

func (p *failFirstPublisher) Publish(_ context.Context, topic string, _ []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topics = append(p.topics, topic)
	if len(p.topics) == 1 {
		return errors.New("broker rejected first event")
	}
	return nil
}

func TestOutboxBatchContinuesAfterRecordedFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 8, 30, 0, 0, time.UTC)
	first, second := testEvent("event-first"), testEvent("event-second")
	first.Topic, second.Topic = "trip.failed", "trip.completed"
	repository := &fakeOutboxRepository{claimed: append([]job.Outbox(nil), first, second)}
	publisher := &failFirstPublisher{}
	worker := NewOutbox(repository, publisher, clock.NewManual(now), workerLogger(), "worker-batch", time.Second, 10*time.Second)

	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process outbox batch: %v", err)
	}
	publisher.mu.Lock()
	gotTopics := append([]string(nil), publisher.topics...)
	publisher.mu.Unlock()
	if len(gotTopics) != 2 || gotTopics[0] != first.Topic || gotTopics[1] != second.Topic {
		t.Fatalf("publisher calls=%v, want failed first event followed by second event", gotTopics)
	}
	if len(repository.failed) != 1 || repository.failed[0] != first.ID {
		t.Fatalf("failed events=%v, want only %s", repository.failed, first.ID)
	}
	if len(repository.completed) != 1 || repository.completed[0] != second.ID {
		t.Fatalf("completed events=%v, want only %s", repository.completed, second.ID)
	}
}
