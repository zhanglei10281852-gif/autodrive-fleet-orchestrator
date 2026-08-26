package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/telemetry"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/repository"
)

func (s *Store) CommitTelemetry(ctx context.Context, commit repository.TelemetryCommit) (bool, error) {
	duplicate := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO telemetry_samples(event_id, vehicle_id, observed_at, latitude, longitude,
				speed_kph, battery_percent, odometer_meters, fault_code, severity, payload_hash, received_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(event_id) DO NOTHING`,
			commit.Sample.EventID, commit.Sample.VehicleID, formatTime(commit.Sample.ObservedAt),
			commit.Sample.Latitude, commit.Sample.Longitude, commit.Sample.SpeedKPH,
			commit.Sample.BatteryPercent, commit.Sample.OdometerMeters, commit.Sample.FaultCode,
			commit.Sample.Severity, commit.Sample.PayloadHash, formatTime(commit.Sample.ReceivedAt))
		if err != nil {
			return mapSQLError(err, "telemetry")
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			var existingHash string
			if err := tx.QueryRowContext(ctx, `SELECT payload_hash FROM telemetry_samples WHERE event_id = ?`, commit.Sample.EventID).Scan(&existingHash); err != nil {
				return mapSQLError(err, "telemetry duplicate")
			}
			if existingHash != commit.Sample.PayloadHash {
				return common.ConflictError{Resource: "telemetry", Reason: "event id was reused with different content"}
			}
			duplicate = true
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE vehicles SET latitude = ?, longitude = ?, battery_percent = ?,
				last_telemetry_at = ?, version = version + 1, updated_at = ?
			WHERE id = ? AND (last_telemetry_at IS NULL OR last_telemetry_at < ?)`,
			commit.Sample.Latitude, commit.Sample.Longitude, commit.Sample.BatteryPercent,
			formatTime(commit.Sample.ObservedAt), formatTime(commit.Sample.ReceivedAt),
			commit.Sample.VehicleID, formatTime(commit.Sample.ObservedAt)); err != nil {
			return mapSQLError(err, "vehicle telemetry snapshot")
		}
		if commit.Incident != nil {
			if err := insertIncident(ctx, tx, *commit.Incident); err != nil {
				return err
			}
		}
		if commit.Outbox != nil {
			if err := insertOutbox(ctx, tx, *commit.Outbox); err != nil {
				return err
			}
		}
		if commit.Audit != nil {
			if err := insertAudit(ctx, tx, *commit.Audit); err != nil {
				return err
			}
		}
		return nil
	})
	return duplicate, err
}

func (s *Store) TelemetryByEventID(ctx context.Context, eventID string) (telemetry.Sample, error) {
	var value telemetry.Sample
	var observed, received, severity string
	err := s.db.QueryRowContext(ctx, `
		SELECT event_id, vehicle_id, observed_at, latitude, longitude, speed_kph,
			battery_percent, odometer_meters, fault_code, severity, payload_hash, received_at
		FROM telemetry_samples WHERE event_id = ?`, eventID).Scan(
		&value.EventID, &value.VehicleID, &observed, &value.Latitude, &value.Longitude,
		&value.SpeedKPH, &value.BatteryPercent, &value.OdometerMeters, &value.FaultCode,
		&severity, &value.PayloadHash, &received)
	if err != nil {
		return telemetry.Sample{}, mapSQLError(err, "telemetry")
	}
	value.Severity = telemetry.Severity(severity)
	if value.ObservedAt, err = parseTime(observed); err != nil {
		return telemetry.Sample{}, err
	}
	if value.ReceivedAt, err = parseTime(received); err != nil {
		return telemetry.Sample{}, err
	}
	return value, nil
}

func (s *Store) RecentTelemetry(ctx context.Context, vehicleID string, since time.Time, limit int) ([]telemetry.Sample, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, vehicle_id, observed_at, latitude, longitude, speed_kph,
			battery_percent, odometer_meters, fault_code, severity, payload_hash, received_at
		FROM telemetry_samples WHERE vehicle_id = ? AND observed_at >= ?
		ORDER BY observed_at DESC LIMIT ?`, vehicleID, formatTime(since), limit)
	if err != nil {
		return nil, mapSQLError(err, "telemetry list")
	}
	defer rows.Close()
	items := make([]telemetry.Sample, 0, limit)
	for rows.Next() {
		var value telemetry.Sample
		var observed, received, severity string
		if err := rows.Scan(&value.EventID, &value.VehicleID, &observed, &value.Latitude,
			&value.Longitude, &value.SpeedKPH, &value.BatteryPercent, &value.OdometerMeters,
			&value.FaultCode, &severity, &value.PayloadHash, &received); err != nil {
			return nil, mapSQLError(err, "telemetry")
		}
		value.Severity = telemetry.Severity(severity)
		var err error
		if value.ObservedAt, err = parseTime(observed); err != nil {
			return nil, err
		}
		if value.ReceivedAt, err = parseTime(received); err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, rows.Err()
}

func (s *Store) TelemetryEventExists(ctx context.Context, eventID string) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM telemetry_samples WHERE event_id = ?)`, eventID).Scan(&exists); err != nil {
		return false, mapSQLError(err, "telemetry existence")
	}
	return exists == 1, nil
}

func (s *Store) VehicleTelemetryWindow(ctx context.Context, vehicleID string) (time.Time, time.Time, error) {
	var first, last sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT MIN(observed_at), MAX(observed_at) FROM telemetry_samples WHERE vehicle_id = ?`, vehicleID).Scan(&first, &last); err != nil {
		return time.Time{}, time.Time{}, mapSQLError(err, "telemetry window")
	}
	if !first.Valid || !last.Valid {
		return time.Time{}, time.Time{}, common.ErrNotFound
	}
	start, err := parseTime(first.String)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := parseTime(last.String)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, end, nil
}

func (s *Store) DeleteTelemetryBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM telemetry_samples WHERE event_id IN (
			SELECT event_id FROM telemetry_samples t
			WHERE observed_at < ? AND NOT EXISTS (
				SELECT 1 FROM safety_incidents i WHERE i.telemetry_event_id = t.event_id
			) ORDER BY observed_at LIMIT ?
		)`, formatTime(before), limit)
	if err != nil {
		return 0, mapSQLError(err, "telemetry retention")
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read telemetry retention result: %w", err)
	}
	return count, nil
}
