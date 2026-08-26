package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

func (s *Store) Enqueue(ctx context.Context, value job.Outbox) error {
	return insertOutbox(ctx, s.db, value)
}

func insertOutbox(ctx context.Context, q Queryer, value job.Outbox) error {
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO outbox_events(id, topic, aggregate_type, aggregate_id, payload, status,
			attempt, max_attempts, available_at, lease_owner, lease_until, last_error, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ID, value.Topic, value.AggregateType, value.AggregateID, string(value.Payload), value.Status,
		value.Attempt, value.MaxAttempts, formatTime(value.AvailableAt), value.LeaseOwner,
		nullableTime(value.LeaseUntil), value.LastError, formatTime(value.CreatedAt), formatTime(value.UpdatedAt))
	return mapSQLError(err, "outbox event")
}

func (s *Store) OutboxByID(ctx context.Context, id string) (job.Outbox, error) {
	return scanOutbox(s.db.QueryRowContext(ctx, outboxSelect+" WHERE id = ?", id))
}

const outboxSelect = `SELECT id, topic, aggregate_type, aggregate_id, payload, status,
	attempt, max_attempts, available_at, lease_owner, lease_until, last_error, created_at, updated_at FROM outbox_events`

func scanOutbox(row *sql.Row) (job.Outbox, error) {
	var value job.Outbox
	var payload string
	var status, available, created, updated string
	var lease sql.NullString
	if err := row.Scan(&value.ID, &value.Topic, &value.AggregateType, &value.AggregateID,
		&payload, &status, &value.Attempt, &value.MaxAttempts, &available,
		&value.LeaseOwner, &lease, &value.LastError, &created, &updated); err != nil {
		return job.Outbox{}, mapSQLError(err, "outbox event")
	}
	value.Status = job.Status(status)
	value.Payload = []byte(payload)
	var err error
	if value.AvailableAt, err = parseTime(available); err != nil {
		return job.Outbox{}, err
	}
	if value.LeaseUntil, err = parseNullableTime(lease); err != nil {
		return job.Outbox{}, err
	}
	if value.CreatedAt, err = parseTime(created); err != nil {
		return job.Outbox{}, err
	}
	if value.UpdatedAt, err = parseTime(updated); err != nil {
		return job.Outbox{}, err
	}
	return value, nil
}

func (s *Store) ClaimOutbox(ctx context.Context, owner string, now time.Time, lease time.Duration, limit int) ([]job.Outbox, error) {
	if owner == "" || lease <= 0 {
		return nil, common.ErrInvalid
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	claimed := make([]job.Outbox, 0, limit)
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, outboxSelect+`
			WHERE (status = 'pending' AND available_at <= ?)
			   OR (status = 'leased' AND lease_until <= ?)
			ORDER BY available_at, created_at LIMIT ?`, formatTime(now), formatTime(now), limit)
		if err != nil {
			return mapSQLError(err, "outbox claim query")
		}
		candidates := make([]job.Outbox, 0, limit)
		for rows.Next() {
			value, err := scanOutboxRows(rows)
			if err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, value)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close outbox claim rows: %w", err)
		}
		for _, candidate := range candidates {
			leased, err := candidate.Lease(owner, now, lease)
			if err != nil {
				continue
			}
			result, err := tx.ExecContext(ctx, `
				UPDATE outbox_events SET status = ?, attempt = ?, lease_owner = ?, lease_until = ?, updated_at = ?
				WHERE id = ? AND ((status = 'pending' AND available_at <= ?) OR (status = 'leased' AND lease_until <= ?))`,
				leased.Status, leased.Attempt, leased.LeaseOwner, nullableTime(leased.LeaseUntil),
				formatTime(leased.UpdatedAt), leased.ID, formatTime(now), formatTime(now))
			if err != nil {
				return mapSQLError(err, "outbox lease")
			}
			changed, _ := result.RowsAffected()
			if changed == 1 {
				claimed = append(claimed, leased)
			}
		}
		return nil
	})
	return claimed, err
}

func scanOutboxRows(rows *sql.Rows) (job.Outbox, error) {
	var value job.Outbox
	var payload string
	var status, available, created, updated string
	var lease sql.NullString
	if err := rows.Scan(&value.ID, &value.Topic, &value.AggregateType, &value.AggregateID,
		&payload, &status, &value.Attempt, &value.MaxAttempts, &available,
		&value.LeaseOwner, &lease, &value.LastError, &created, &updated); err != nil {
		return job.Outbox{}, mapSQLError(err, "outbox event")
	}
	value.Status = job.Status(status)
	value.Payload = []byte(payload)
	var err error
	value.AvailableAt, err = parseTime(available)
	if err != nil {
		return job.Outbox{}, err
	}
	value.LeaseUntil, err = parseNullableTime(lease)
	if err != nil {
		return job.Outbox{}, err
	}
	value.CreatedAt, err = parseTime(created)
	if err != nil {
		return job.Outbox{}, err
	}
	value.UpdatedAt, err = parseTime(updated)
	return value, err
}

func (s *Store) CompleteOutbox(ctx context.Context, id, owner string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbox_events SET status = 'completed', lease_owner = '', lease_until = NULL,
			last_error = '', updated_at = ?
		WHERE id = ? AND status = 'leased' AND lease_owner = ?`, formatTime(now), id, owner)
	if err != nil {
		return mapSQLError(err, "outbox completion")
	}
	return rowsAffectedExactlyOne(result, "outbox completion")
}

func (s *Store) FailOutbox(ctx context.Context, id, owner string, now time.Time, backoff time.Duration, cause error) error {
	current, err := s.OutboxByID(ctx, id)
	if err != nil {
		return err
	}
	var deliveryError job.DeliveryError
	if errors.As(cause, &deliveryError) && deliveryError.Permanent {
		current.Attempt = current.MaxAttempts
	}
	failed, err := current.Fail(owner, cause, now, backoff)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbox_events SET status = ?, available_at = ?, lease_owner = '', lease_until = NULL,
			last_error = ?, updated_at = ? WHERE id = ? AND status = 'leased' AND lease_owner = ?`,
		failed.Status, formatTime(failed.AvailableAt), failed.LastError, formatTime(failed.UpdatedAt), id, owner)
	if err != nil {
		return mapSQLError(err, "outbox failure")
	}
	return rowsAffectedExactlyOne(result, "outbox failure")
}

func (s *Store) OutboxCounts(ctx context.Context) (map[job.Status]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM outbox_events GROUP BY status`)
	if err != nil {
		return nil, mapSQLError(err, "outbox counts")
	}
	defer rows.Close()
	counts := make(map[job.Status]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan outbox counts: %w", err)
		}
		counts[job.Status(status)] = count
	}
	return counts, rows.Err()
}
