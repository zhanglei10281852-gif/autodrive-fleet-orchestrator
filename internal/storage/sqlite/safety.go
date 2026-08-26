package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/safety"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/repository"
)

func insertIncident(ctx context.Context, q Queryer, incident safety.Incident) error {
	if err := incident.Validate(); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO safety_incidents(id, vehicle_id, telemetry_event_id, severity, category,
			summary, status, owner_id, lease_until, resolution, version, opened_at, updated_at, closed_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		incident.ID, incident.VehicleID, nullableString(incident.TelemetryEvent), incident.Severity,
		incident.Category, incident.Summary, incident.Status, nullableString(incident.OwnerID),
		nullableTime(incident.LeaseUntil), incident.Resolution, incident.Version,
		formatTime(incident.OpenedAt), formatTime(incident.UpdatedAt), nullableTime(incident.ClosedAt))
	return mapSQLError(err, "safety incident")
}

func (s *Store) CreateIncident(ctx context.Context, incident safety.Incident) error {
	return insertIncident(ctx, s.db, incident)
}

const incidentSelect = `SELECT id, vehicle_id, telemetry_event_id, severity, category, summary,
	status, owner_id, lease_until, resolution, version, opened_at, updated_at, closed_at FROM safety_incidents`

func (s *Store) IncidentByID(ctx context.Context, id string) (safety.Incident, error) {
	return scanIncident(s.db.QueryRowContext(ctx, incidentSelect+" WHERE id = ?", id))
}

func scanIncident(row *sql.Row) (safety.Incident, error) {
	var value safety.Incident
	var telemetryEvent, owner, lease, closed sql.NullString
	var severity, status, opened, updated string
	if err := row.Scan(&value.ID, &value.VehicleID, &telemetryEvent, &severity, &value.Category,
		&value.Summary, &status, &owner, &lease, &value.Resolution, &value.Version,
		&opened, &updated, &closed); err != nil {
		return safety.Incident{}, mapSQLError(err, "safety incident")
	}
	value.TelemetryEvent = telemetryEvent.String
	value.OwnerID = owner.String
	value.Severity = safety.Severity(severity)
	value.Status = safety.Status(status)
	var err error
	if value.LeaseUntil, err = parseNullableTime(lease); err != nil {
		return safety.Incident{}, err
	}
	if value.ClosedAt, err = parseNullableTime(closed); err != nil {
		return safety.Incident{}, err
	}
	if value.OpenedAt, err = parseTime(opened); err != nil {
		return safety.Incident{}, err
	}
	if value.UpdatedAt, err = parseTime(updated); err != nil {
		return safety.Incident{}, err
	}
	return value, nil
}

func (s *Store) ListIncidents(ctx context.Context, filter safety.Filter) (common.Page[safety.Incident], error) {
	page := filter.Page.Normalize()
	conditions := []string{"1 = 1"}
	args := make([]any, 0, 6)
	if filter.VehicleID != "" {
		conditions = append(conditions, "vehicle_id = ?")
		args = append(args, filter.VehicleID)
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.Severity != "" {
		conditions = append(conditions, "severity = ?")
		args = append(args, filter.Severity)
	}
	if filter.OwnerID != "" {
		conditions = append(conditions, "owner_id = ?")
		args = append(args, filter.OwnerID)
	}
	where := strings.Join(conditions, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM safety_incidents WHERE "+where, args...).Scan(&total); err != nil {
		return common.Page[safety.Incident]{}, mapSQLError(err, "incident count")
	}
	queryArgs := append(append([]any(nil), args...), page.Limit, page.Offset)
	rows, err := s.db.QueryContext(ctx, incidentSelect+" WHERE "+where+` ORDER BY
		CASE severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 ELSE 4 END,
		opened_at ASC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return common.Page[safety.Incident]{}, mapSQLError(err, "incident list")
	}
	defer rows.Close()
	items := make([]safety.Incident, 0, page.Limit)
	for rows.Next() {
		value, err := scanIncidentRows(rows)
		if err != nil {
			return common.Page[safety.Incident]{}, err
		}
		items = append(items, value)
	}
	return common.NewPage(items, total, page), rows.Err()
}

func scanIncidentRows(rows *sql.Rows) (safety.Incident, error) {
	var value safety.Incident
	var telemetryEvent, owner, lease, closed sql.NullString
	var severity, status, opened, updated string
	if err := rows.Scan(&value.ID, &value.VehicleID, &telemetryEvent, &severity, &value.Category,
		&value.Summary, &status, &owner, &lease, &value.Resolution, &value.Version,
		&opened, &updated, &closed); err != nil {
		return safety.Incident{}, mapSQLError(err, "safety incident")
	}
	value.TelemetryEvent = telemetryEvent.String
	value.OwnerID = owner.String
	value.Severity = safety.Severity(severity)
	value.Status = safety.Status(status)
	var err error
	value.LeaseUntil, err = parseNullableTime(lease)
	if err != nil {
		return safety.Incident{}, err
	}
	value.ClosedAt, err = parseNullableTime(closed)
	if err != nil {
		return safety.Incident{}, err
	}
	value.OpenedAt, err = parseTime(opened)
	if err != nil {
		return safety.Incident{}, err
	}
	value.UpdatedAt, err = parseTime(updated)
	return value, err
}

func (s *Store) ClaimIncident(ctx context.Context, id, operatorID string, now time.Time, lease time.Duration, expectedVersion int64) (safety.Incident, error) {
	current, err := s.IncidentByID(ctx, id)
	if err != nil {
		return safety.Incident{}, err
	}
	claimed, err := current.Claim(operatorID, now, lease)
	if err != nil {
		return safety.Incident{}, err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE safety_incidents SET status = ?, owner_id = ?, lease_until = ?, version = ?, updated_at = ?
		WHERE id = ? AND version = ? AND (owner_id IS NULL OR owner_id = ? OR lease_until <= ?)`,
		claimed.Status, claimed.OwnerID, nullableTime(claimed.LeaseUntil), claimed.Version,
		formatTime(now), id, expectedVersion, operatorID, formatTime(now))
	if err != nil {
		return safety.Incident{}, mapSQLError(err, "incident claim")
	}
	if err := rowsAffectedExactlyOne(result, "incident claim"); err != nil {
		return safety.Incident{}, err
	}
	claimed.UpdatedAt = now.UTC()
	return claimed, nil
}

func (s *Store) CommitIncidentResolution(ctx context.Context, commit repository.IncidentResolutionCommit) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if commit.Vehicle.ID != commit.Incident.VehicleID {
			return common.ConflictError{Resource: "vehicle", Reason: "incident vehicle snapshot does not match"}
		}
		switch commit.Vehicle.Status {
		case fleet.VehicleSuspended, fleet.VehicleMaintenance, fleet.VehicleOffline:
		default:
			return common.ConflictError{Resource: "vehicle", Reason: "vehicle snapshot is not in a safe state"}
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE safety_incidents SET status = ?, owner_id = ?, lease_until = NULL,
				resolution = ?, version = ?, updated_at = ?
			WHERE id = ? AND version = ? AND status = 'mitigating'`,
			commit.Incident.Status, commit.Incident.OwnerID, commit.Incident.Resolution,
			commit.Incident.Version, formatTime(commit.Incident.UpdatedAt), commit.Incident.ID,
			commit.ExpectedIncidentVersion)
		if err != nil {
			return mapSQLError(err, "incident resolution")
		}
		if err := rowsAffectedExactlyOne(result, "incident resolution"); err != nil {
			return err
		}
		return insertAudit(ctx, tx, commit.Audit)
	})
}

func (s *Store) UpdateIncident(ctx context.Context, incident safety.Incident, expectedVersion int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE safety_incidents SET status = ?, owner_id = ?, lease_until = ?, resolution = ?,
			version = ?, updated_at = ?, closed_at = ? WHERE id = ? AND version = ?`,
		incident.Status, nullableString(incident.OwnerID), nullableTime(incident.LeaseUntil),
		incident.Resolution, incident.Version, formatTime(incident.UpdatedAt), nullableTime(incident.ClosedAt),
		incident.ID, expectedVersion)
	if err != nil {
		return mapSQLError(err, "incident")
	}
	return rowsAffectedExactlyOne(result, "incident")
}
