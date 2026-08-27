package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
)

func (s *Store) AppendAudit(ctx context.Context, event audit.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	return insertAudit(ctx, s.db, event)
}

func insertAudit(ctx context.Context, q Queryer, event audit.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO audit_events(id, actor_id, actor_role, action, object_type, object_id,
			result, request_id, details, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.ActorID, event.ActorRole, event.Action, event.ObjectType,
		event.ObjectID, event.Result, event.RequestID, string(event.Details), formatTime(event.CreatedAt))
	return mapSQLError(err, "audit event")
}

func (s *Store) ListAudit(ctx context.Context, filter audit.Filter) (common.Page[audit.Event], error) {
	page := filter.Page.Normalize()
	conditions := []string{"1 = 1"}
	args := make([]any, 0, 10)
	if filter.ActorID != "" {
		conditions = append(conditions, "actor_id = ?")
		args = append(args, filter.ActorID)
	}
	if filter.Action != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, filter.Action)
	}
	if filter.ObjectType != "" {
		conditions = append(conditions, "object_type = ?")
		args = append(args, filter.ObjectType)
	}
	if filter.ObjectID != "" {
		conditions = append(conditions, "object_id = ?")
		args = append(args, filter.ObjectID)
	}
	if filter.Result != "" {
		conditions = append(conditions, "result = ?")
		args = append(args, filter.Result)
	}
	if filter.From != nil {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, formatTime(*filter.From))
	}
	if filter.To != nil {
		conditions = append(conditions, "created_at < ?")
		args = append(args, formatTime(*filter.To))
	}
	where := strings.Join(conditions, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE "+where, args...).Scan(&total); err != nil {
		return common.Page[audit.Event]{}, mapSQLError(err, "audit count")
	}
	queryArgs := append(append([]any(nil), args...), page.Limit, page.Offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, actor_id, actor_role, action, object_type, object_id, result, request_id, details, created_at
		FROM audit_events WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return common.Page[audit.Event]{}, mapSQLError(err, "audit list")
	}
	defer rows.Close()
	items := make([]audit.Event, 0, page.Limit)
	for rows.Next() {
		var event audit.Event
		var details string
		var result, created string
		if err := rows.Scan(&event.ID, &event.ActorID, &event.ActorRole, &event.Action,
			&event.ObjectType, &event.ObjectID, &result, &event.RequestID, &details, &created); err != nil {
			return common.Page[audit.Event]{}, mapSQLError(err, "audit event")
		}
		event.Result = audit.Result(result)
		event.Details = []byte(details)
		var err error
		if event.CreatedAt, err = parseTime(created); err != nil {
			return common.Page[audit.Event]{}, err
		}
		items = append(items, event)
	}
	if err := rows.Err(); err != nil {
		return common.Page[audit.Event]{}, mapSQLError(err, "audit list")
	}
	return common.NewPage(items, total, page), nil
}

func (s *Store) AuditEventByID(ctx context.Context, id string) (audit.Event, error) {
	var event audit.Event
	var details string
	var result, created string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, actor_id, actor_role, action, object_type, object_id, result, request_id, details, created_at
		FROM audit_events WHERE id = ?`, id).Scan(
		&event.ID, &event.ActorID, &event.ActorRole, &event.Action, &event.ObjectType,
		&event.ObjectID, &result, &event.RequestID, &details, &created)
	if err != nil {
		return audit.Event{}, mapSQLError(err, "audit event")
	}
	event.Result = audit.Result(result)
	event.Details = []byte(details)
	if event.CreatedAt, err = parseTime(created); err != nil {
		return audit.Event{}, err
	}
	return event, nil
}

func (s *Store) AuditCountForRequest(ctx context.Context, requestID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_events WHERE request_id = ?", requestID).Scan(&count); err != nil {
		return 0, mapSQLError(err, "audit count")
	}
	return count, nil
}

var _ Queryer = (*sql.Tx)(nil)
