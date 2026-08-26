package worker

import (
	"context"
	"fmt"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

func publishOutboxEvent(ctx context.Context, publisher Publisher, event job.Outbox) error {
	if err := publisher.Publish(ctx, event.Topic, append([]byte(nil), event.Payload...)); err != nil {
		return fmt.Errorf("publish outbox event %s: %v", event.ID, err)
	}
	return nil
}
