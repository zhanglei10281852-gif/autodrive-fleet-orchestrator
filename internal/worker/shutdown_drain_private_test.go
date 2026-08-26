package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

type shutdownDrainRepository struct {
	mu        sync.Mutex
	claimed   bool
	started   chan struct{}
	completed []string
}

func (r *shutdownDrainRepository) ClaimOutbox(_ context.Context, owner string, now time.Time, lease time.Duration, _ int) ([]job.Outbox, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed {
		return nil, nil
	}
	r.claimed = true
	close(r.started)
	until := now.Add(lease)
	return []job.Outbox{{ID: "shutdown-event", Topic: "trip.completed", Payload: []byte(`{"trip":"shutdown"}`), Status: job.StatusLeased, LeaseOwner: owner, LeaseUntil: &until, Attempt: 1, MaxAttempts: 3}}, nil
}

func (r *shutdownDrainRepository) CompleteOutbox(_ context.Context, id, _ string, _ time.Time) error {
	r.mu.Lock()
	r.completed = append(r.completed, id)
	r.mu.Unlock()
	return nil
}

func (r *shutdownDrainRepository) FailOutbox(context.Context, string, string, time.Time, time.Duration, error) error {
	return nil
}

type shutdownDrainPublisher struct {
	started chan struct{}
	release chan struct{}
}

func (p *shutdownDrainPublisher) Publish(_ context.Context, _ string, _ []byte) error {
	close(p.started)
	<-p.release
	return nil
}

func TestOutboxShutdownDrainsInflightCycle(t *testing.T) {
	repository := &shutdownDrainRepository{started: make(chan struct{})}
	publisher := &shutdownDrainPublisher{started: make(chan struct{}), release: make(chan struct{})}
	worker := NewOutbox(repository, publisher, clock.System{}, slog.New(slog.NewTextHandler(io.Discard, nil)), "shutdown-worker", 5*time.Millisecond, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- worker.Run(ctx) }()
	select {
	case <-publisher.started:
	case <-time.After(time.Second):
		t.Fatal("outbox cycle did not start publishing")
	}
	cancel()
	select {
	case err := <-runDone:
		t.Fatalf("worker returned %v before the in-flight delivery drained", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(publisher.release)
	select {
	case err := <-runDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown error=%v want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not return after delivery drain")
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.completed) != 1 || repository.completed[0] != "shutdown-event" {
		t.Fatalf("in-flight event was not durably completed: %v", repository.completed)
	}
}
