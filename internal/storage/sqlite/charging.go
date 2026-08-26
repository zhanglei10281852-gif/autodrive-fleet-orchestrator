package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/charging"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/job"
)

func (s *Store) CreateStation(ctx context.Context, station charging.Station) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO charging_stations(id, region_id, code, name, active, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		station.ID, station.RegionID, station.Code, station.Name, boolInt(station.Active),
		formatTime(station.CreatedAt), formatTime(station.UpdatedAt))
	return mapSQLError(err, "charging station")
}

func (s *Store) CreateConnector(ctx context.Context, connector charging.Connector) error {
	if err := connector.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO charging_connectors(id, station_id, code, power_kw, active, version, lease_owner_id, lease_until)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, connector.ID, connector.StationID, connector.Code,
		connector.PowerKW, boolInt(connector.Active), connector.Version, connector.LeaseOwnerID,
		nullableTime(connector.LeaseUntil))
	return mapSQLError(err, "charging connector")
}

func (s *Store) ConnectorByID(ctx context.Context, id string) (charging.Connector, error) {
	var value charging.Connector
	var active int
	var lease sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, station_id, code, power_kw, active, version, lease_owner_id, lease_until
		FROM charging_connectors WHERE id = ?`, id).Scan(&value.ID, &value.StationID,
		&value.Code, &value.PowerKW, &active, &value.Version, &value.LeaseOwnerID, &lease)
	if err != nil {
		return charging.Connector{}, mapSQLError(err, "charging connector")
	}
	value.Active = active == 1
	if value.LeaseUntil, err = parseNullableTime(lease); err != nil {
		return charging.Connector{}, err
	}
	return value, nil
}

func (s *Store) CreateChargingSession(ctx context.Context, session charging.Session, event audit.Event) error {
	if err := session.Validate(session.CreatedAt); err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		var conflicts int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM charging_sessions
			WHERE connector_id = ? AND status IN ('reserved', 'active')
			  AND window_start < ? AND ? < window_end`,
			session.ConnectorID, formatTime(session.WindowEnd), formatTime(session.WindowStart)).Scan(&conflicts); err != nil {
			return mapSQLError(err, "charging conflict")
		}
		if conflicts > 0 {
			return common.ConflictError{Resource: "charging connector", Reason: "reservation window overlaps an active session"}
		}
		var vehicleStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM vehicles WHERE id = ?`, session.VehicleID).Scan(&vehicleStatus); err != nil {
			return mapSQLError(err, "charging vehicle")
		}
		if vehicleStatus != string(fleet.VehicleAvailable) {
			return common.ConflictError{Resource: "vehicle", Reason: "vehicle is not available for charging"}
		}
		if _, err := tx.ExecContext(ctx, chargingSessionInsert,
			session.ID, session.VehicleID, session.ConnectorID, session.Status,
			formatTime(session.WindowStart), formatTime(session.WindowEnd), nil, nil,
			session.InitialBattery, nil, session.EnergyWattHours, session.IdempotencyKey,
			session.Version, session.CreatedBy, formatTime(session.CreatedAt)); err != nil {
			return mapSQLError(err, "charging session")
		}
		return insertAudit(ctx, tx, event)
	})
}

const chargingSessionInsert = `INSERT INTO charging_sessions(
	id, vehicle_id, connector_id, status, window_start, window_end, started_at, completed_at,
	initial_battery, final_battery, energy_watt_hours, idempotency_key, version, created_by, created_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const chargingSessionSelect = `SELECT id, vehicle_id, connector_id, status, window_start,
	window_end, started_at, completed_at, initial_battery, final_battery, energy_watt_hours,
	idempotency_key, version, created_by, created_at FROM charging_sessions`

func (s *Store) ChargingSessionByID(ctx context.Context, id string) (charging.Session, error) {
	return scanChargingSession(s.db.QueryRowContext(ctx, chargingSessionSelect+" WHERE id = ?", id))
}

func (s *Store) ChargingSessionByIdempotency(ctx context.Context, actorID, key string) (charging.Session, error) {
	return scanChargingSession(s.db.QueryRowContext(ctx, chargingSessionSelect+" WHERE created_by = ? AND idempotency_key = ?", actorID, key))
}

func scanChargingSession(row *sql.Row) (charging.Session, error) {
	var value charging.Session
	var status, start, end, created string
	var started, completed sql.NullString
	var final sql.NullInt64
	if err := row.Scan(&value.ID, &value.VehicleID, &value.ConnectorID, &status, &start,
		&end, &started, &completed, &value.InitialBattery, &final, &value.EnergyWattHours,
		&value.IdempotencyKey, &value.Version, &value.CreatedBy, &created); err != nil {
		return charging.Session{}, mapSQLError(err, "charging session")
	}
	value.Status = charging.Status(status)
	if final.Valid {
		battery := int(final.Int64)
		value.FinalBattery = &battery
	}
	var err error
	if value.WindowStart, err = parseTime(start); err != nil {
		return charging.Session{}, err
	}
	if value.WindowEnd, err = parseTime(end); err != nil {
		return charging.Session{}, err
	}
	if value.StartedAt, err = parseNullableTime(started); err != nil {
		return charging.Session{}, err
	}
	if value.CompletedAt, err = parseNullableTime(completed); err != nil {
		return charging.Session{}, err
	}
	if value.CreatedAt, err = parseTime(created); err != nil {
		return charging.Session{}, err
	}
	return value, nil
}

func (s *Store) StartCharging(ctx context.Context, session charging.Session, connector charging.Connector, vehicleVersion int64, at time.Time, event audit.Event) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		sessionResult, err := tx.ExecContext(ctx, `
			UPDATE charging_sessions SET status = 'active', started_at = ?, version = ?
			WHERE id = ? AND status = 'reserved' AND version = ?`,
			nullableTime(session.StartedAt), session.Version, session.ID, session.Version-1)
		if err != nil {
			return mapSQLError(err, "charging start")
		}
		if err := rowsAffectedExactlyOne(sessionResult, "charging start"); err != nil {
			return err
		}
		connectorResult, err := tx.ExecContext(ctx, `
			UPDATE charging_connectors SET lease_owner_id = ?, lease_until = ?, version = version + 1
			WHERE id = ? AND active = 1 AND version = ?
			  AND (lease_until IS NULL OR lease_until <= ? OR lease_owner_id = ?)`,
			session.ID, formatTime(session.WindowEnd), connector.ID, connector.Version, formatTime(at), session.ID)
		if err != nil {
			return mapSQLError(err, "connector lease")
		}
		if err := rowsAffectedExactlyOne(connectorResult, "connector lease"); err != nil {
			return err
		}
		vehicleResult, err := tx.ExecContext(ctx, `
			UPDATE vehicles SET status = 'charging', version = version + 1, updated_at = ?
			WHERE id = ? AND status = 'available' AND version = ?`, formatTime(at), session.VehicleID, vehicleVersion)
		if err != nil {
			return mapSQLError(err, "charging vehicle")
		}
		if err := rowsAffectedExactlyOne(vehicleResult, "charging vehicle"); err != nil {
			return err
		}
		return insertAudit(ctx, tx, event)
	})
}

func (s *Store) CompleteCharging(ctx context.Context, session charging.Session, vehicleVersion int64, at time.Time, event audit.Event, outbox job.Outbox) error {
	if session.FinalBattery == nil {
		return common.ErrInvalid
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		sessionResult, err := tx.ExecContext(ctx, `
			UPDATE charging_sessions SET status = 'completed', completed_at = ?, final_battery = ?,
				energy_watt_hours = ?, version = ? WHERE id = ? AND status = 'active' AND version = ?`,
			nullableTime(session.CompletedAt), *session.FinalBattery, session.EnergyWattHours,
			session.Version, session.ID, session.Version-1)
		if err != nil {
			return mapSQLError(err, "charging completion")
		}
		if err := rowsAffectedExactlyOne(sessionResult, "charging completion"); err != nil {
			return err
		}
		connectorResult, err := tx.ExecContext(ctx, `
			UPDATE charging_connectors SET lease_owner_id = '', lease_until = NULL, version = version + 1
			WHERE id = ? AND lease_owner_id = ?`, session.ConnectorID, session.ID)
		if err != nil {
			return mapSQLError(err, "connector release")
		}
		if err := rowsAffectedExactlyOne(connectorResult, "connector release"); err != nil {
			return err
		}
		vehicleResult, err := tx.ExecContext(ctx, `
			UPDATE vehicles SET status = 'available', battery_percent = ?, version = version + 1, updated_at = ?
			WHERE id = ? AND status = 'charging' AND version = ?`, *session.FinalBattery,
			formatTime(at), session.VehicleID, vehicleVersion)
		if err != nil {
			return mapSQLError(err, "charged vehicle")
		}
		if err := rowsAffectedExactlyOne(vehicleResult, "charged vehicle"); err != nil {
			return err
		}
		return insertAudit(ctx, tx, event)
	}); err != nil {
		return err
	}
	return s.insertChargingCompletionOutbox(ctx, outbox)
}

func (s *Store) ExpireChargingReservations(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE charging_sessions SET status = 'expired', version = version + 1
		WHERE id IN (SELECT id FROM charging_sessions WHERE status = 'reserved' AND window_end <= ? ORDER BY window_end LIMIT ?)`,
		formatTime(now), limit)
	if err != nil {
		return 0, mapSQLError(err, "charging expiration")
	}
	return result.RowsAffected()
}
