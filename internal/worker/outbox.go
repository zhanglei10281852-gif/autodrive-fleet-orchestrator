package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/clock"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

type OutboxRepository interface {
	ClaimOutbox(context.Context, string, time.Time, time.Duration, int) ([]job.Outbox, error)
	CompleteOutbox(context.Context, string, string, time.Time) error
	FailOutbox(context.Context, string, string, time.Time, time.Duration, error) error
}

type Publisher interface {
	Publish(context.Context, string, []byte) error
}

type Outbox struct {
	repository OutboxRepository
	publisher  Publisher
	clock      clock.Clock
	logger     *slog.Logger
	owner      string
	interval   time.Duration
	lease      time.Duration
	batch      int
	wg         sync.WaitGroup
}

func NewOutbox(repository OutboxRepository, publisher Publisher, businessClock clock.Clock, logger *slog.Logger, owner string, interval, lease time.Duration) *Outbox {
	return &Outbox{repository: repository, publisher: publisher, clock: businessClock, logger: logger,
		owner: owner, interval: interval, lease: lease, batch: 20}
}

func (w *Outbox) Run(ctx context.Context) error {
	if w.owner == "" || w.interval <= 0 || w.lease <= w.interval {
		return fmt.Errorf("invalid outbox worker configuration")
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.wg.Wait()
			return ctx.Err()
		case <-ticker.C:
			go func() {
				if err := w.ProcessOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
					w.logger.ErrorContext(ctx, "outbox cycle failed", "error", err)
				}
			}()
		}
	}
}

func (w *Outbox) ProcessOnce(ctx context.Context) error {
	claimed, err := w.repository.ClaimOutbox(ctx, w.owner, w.clock.Now(), w.lease, w.batch)
	if err != nil {
		return fmt.Errorf("claim outbox: %w", err)
	}
	for _, event := range claimed {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.deliver(ctx, event); err != nil {
			w.logger.WarnContext(ctx, "outbox delivery recorded as failed", "event_id", event.ID, "topic", event.Topic, "error", err)
		}
	}
	return nil
}

func (w *Outbox) deliver(ctx context.Context, event job.Outbox) error {
	deliveryCtx, cancel := context.WithTimeout(ctx, w.lease/2)
	defer cancel()
	err := w.publisher.Publish(deliveryCtx, event.Topic, append([]byte(nil), event.Payload...))
	if err == nil {
		if completeErr := w.repository.CompleteOutbox(ctx, event.ID, w.owner, w.clock.Now()); completeErr != nil {
			return fmt.Errorf("complete outbox event: %w", completeErr)
		}
		return nil
	}
	backoff := retryBackoff(event.Attempt)
	var deliveryError job.DeliveryError
	if errors.As(err, &deliveryError) && deliveryError.Permanent {
		backoff = 0
	}
	if failErr := w.repository.FailOutbox(ctx, event.ID, w.owner, w.clock.Now(), backoff, err); failErr != nil {
		return fmt.Errorf("record delivery failure after %v: %w", err, failErr)
	}
	return err
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

type LogPublisher struct {
	Logger *slog.Logger
}

func (p LogPublisher) Publish(ctx context.Context, topic string, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.Logger.InfoContext(ctx, "outbox event delivered", "topic", topic, "payload_bytes", len(payload))
	return nil
}
