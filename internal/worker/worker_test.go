package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

type fakeOutboxRepository struct {
	mu        sync.Mutex
	claimed   []job.Outbox
	completed []string
	failed    []string
	claimErr  error
	finishErr error
}

func (f *fakeOutboxRepository) ClaimOutbox(_ context.Context, owner string, now time.Time, lease time.Duration, _ int) ([]job.Outbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	result := append([]job.Outbox(nil), f.claimed...)
	for index := range result {
		result[index].Status = job.StatusLeased
		result[index].LeaseOwner = owner
		until := now.Add(lease)
		result[index].LeaseUntil = &until
		result[index].Attempt++
	}
	f.claimed = nil
	return result, nil
}

func (f *fakeOutboxRepository) CompleteOutbox(_ context.Context, id, _ string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.finishErr != nil {
		return f.finishErr
	}
	f.completed = append(f.completed, id)
	return nil
}

func (f *fakeOutboxRepository) FailOutbox(_ context.Context, id, _ string, _ time.Time, _ time.Duration, _ error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.finishErr != nil {
		return f.finishErr
	}
	f.failed = append(f.failed, id)
	return nil
}

type fakePublisher struct {
	mu       sync.Mutex
	topics   []string
	payloads [][]byte
	err      error
	block    <-chan struct{}
}

func (f *fakePublisher) Publish(ctx context.Context, topic string, payload []byte) error {
	if f.block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.block:
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.topics = append(f.topics, topic)
	f.payloads = append(f.payloads, append([]byte(nil), payload...))
	return f.err
}

func workerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testEvent(id string) job.Outbox {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	return job.Outbox{ID: id, Topic: "trip.completed", AggregateType: "trip", AggregateID: "trip-1",
		Payload: []byte(`{"trip":"trip-1"}`), Status: job.StatusPending, MaxAttempts: 3,
		AvailableAt: now, CreatedAt: now, UpdatedAt: now}
}

func TestOutboxProcessOncePublishesAndCompletes(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	repository := &fakeOutboxRepository{claimed: []job.Outbox{testEvent("e1"), testEvent("e2")}}
	publisher := &fakePublisher{}
	worker := NewOutbox(repository, publisher, clock.NewManual(now), workerLogger(), "worker-a", time.Second, 10*time.Second)
	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("process once: %v", err)
	}
	if len(repository.completed) != 2 || repository.completed[0] != "e1" || repository.completed[1] != "e2" {
		t.Fatalf("unexpected completions: %v", repository.completed)
	}
	if len(repository.failed) != 0 {
		t.Fatalf("successful events marked failed: %v", repository.failed)
	}
	if len(publisher.topics) != 2 || publisher.topics[0] != "trip.completed" {
		t.Fatalf("unexpected publishes: %v", publisher.topics)
	}
	if string(publisher.payloads[0]) != `{"trip":"trip-1"}` {
		t.Fatalf("payload changed: %s", publisher.payloads[0])
	}
}

func TestOutboxProcessOnceRecordsDeliveryFailure(t *testing.T) {
	now := time.Now().UTC()
	repository := &fakeOutboxRepository{claimed: []job.Outbox{testEvent("e1")}}
	publisher := &fakePublisher{err: errors.New("broker unavailable")}
	worker := NewOutbox(repository, publisher, clock.NewManual(now), workerLogger(), "worker-a", time.Second, 10*time.Second)
	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("individual delivery failure should be recorded and cycle continue: %v", err)
	}
	if len(repository.completed) != 0 || len(repository.failed) != 1 || repository.failed[0] != "e1" {
		t.Fatalf("unexpected terminal calls completed=%v failed=%v", repository.completed, repository.failed)
	}
}

func TestOutboxClaimFailureStopsCycle(t *testing.T) {
	repository := &fakeOutboxRepository{claimErr: errors.New("database unavailable")}
	worker := NewOutbox(repository, &fakePublisher{}, clock.System{}, workerLogger(), "worker-a", time.Second, 10*time.Second)
	err := worker.ProcessOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "claim outbox") {
		t.Fatalf("expected wrapped claim error, got %v", err)
	}
}

func TestOutboxCompletionFailureIsReturned(t *testing.T) {
	repository := &fakeOutboxRepository{claimed: []job.Outbox{testEvent("e1")}, finishErr: errors.New("commit failed")}
	worker := NewOutbox(repository, &fakePublisher{}, clock.System{}, workerLogger(), "worker-a", time.Second, 10*time.Second)
	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("delivery error is logged per event and cycle continues: %v", err)
	}
	if len(repository.completed) != 0 {
		t.Fatalf("failed completion recorded: %v", repository.completed)
	}
}

func TestOutboxDeliveryHonorsContextDeadline(t *testing.T) {
	block := make(chan struct{})
	repository := &fakeOutboxRepository{claimed: []job.Outbox{testEvent("e1")}}
	publisher := &fakePublisher{block: block}
	worker := NewOutbox(repository, publisher, clock.System{}, workerLogger(), "worker-a", 5*time.Millisecond, 20*time.Millisecond)
	started := time.Now()
	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("timeout should be recorded as event failure: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("delivery did not respect timeout")
	}
	if len(repository.failed) != 1 {
		t.Fatalf("timed out event not failed: %v", repository.failed)
	}
}

func TestRetryBackoffCapsAtEight(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{-1, time.Second},
		{0, time.Second},
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{8, 128 * time.Second},
		{9, 128 * time.Second},
		{100, 128 * time.Second},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("attempt_%d", test.attempt), func(t *testing.T) {
			if got := retryBackoff(test.attempt); got != test.want {
				t.Fatalf("backoff=%v want %v", got, test.want)
			}
		})
	}
}

type fakeHousekeepingRepository struct {
	sessions     int64
	reservations int64
	telemetry    int64
	errAt        string
	calls        []string
}

func (f *fakeHousekeepingRepository) DeleteExpiredSessions(context.Context, time.Time, int) (int64, error) {
	f.calls = append(f.calls, "sessions")
	if f.errAt == "sessions" {
		return 0, errors.New("sessions failed")
	}
	return f.sessions, nil
}

func (f *fakeHousekeepingRepository) ExpireChargingReservations(context.Context, time.Time, int) (int64, error) {
	f.calls = append(f.calls, "reservations")
	if f.errAt == "reservations" {
		return 0, errors.New("reservations failed")
	}
	return f.reservations, nil
}

func (f *fakeHousekeepingRepository) DeleteTelemetryBefore(context.Context, time.Time, int) (int64, error) {
	f.calls = append(f.calls, "telemetry")
	if f.errAt == "telemetry" {
		return 0, errors.New("telemetry failed")
	}
	return f.telemetry, nil
}

func TestHousekeepingProcessesAllRetentionSteps(t *testing.T) {
	now := time.Now().UTC()
	repository := &fakeHousekeepingRepository{sessions: 1, reservations: 2, telemetry: 3}
	worker := NewHousekeeping(repository, clock.NewManual(now), workerLogger(), time.Minute, 30*24*time.Hour)
	if err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("housekeeping: %v", err)
	}
	want := []string{"sessions", "reservations", "telemetry"}
	if !slices.Equal(repository.calls, want) {
		t.Fatalf("calls=%v want %v", repository.calls, want)
	}
}

func TestHousekeepingStopsAfterFailure(t *testing.T) {
	for _, failure := range []string{"sessions", "reservations", "telemetry"} {
		t.Run(failure, func(t *testing.T) {
			repository := &fakeHousekeepingRepository{errAt: failure}
			worker := NewHousekeeping(repository, clock.System{}, workerLogger(), time.Minute, 24*time.Hour)
			if err := worker.ProcessOnce(context.Background()); err == nil {
				t.Fatal("expected housekeeping error")
			}
			if repository.calls[len(repository.calls)-1] != failure {
				t.Fatalf("continued after failure: %v", repository.calls)
			}
		})
	}
}
