package sqlite

import (
	"context"
	"database/sql"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

func (s *Store) insertChargingCompletionOutbox(ctx context.Context, outbox job.Outbox) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		return insertOutbox(ctx, tx, outbox)
	})
}
