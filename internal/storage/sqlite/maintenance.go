package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/maintenance"
)

func (s *Store) OpenMaintenance(ctx context.Context, order maintenance.WorkOrder, vehicle fleet.Vehicle, event audit.Event) error {
	required, err := encodeStrings(order.RequiredChecks)
	if err != nil {
		return err
	}
	completed, err := encodeStrings(order.CompletedChecks)
	if err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		vehicleResult, err := tx.ExecContext(ctx, `
			UPDATE vehicles SET status = 'maintenance', version = version + 1, updated_at = ?
			WHERE id = ? AND status IN ('offline', 'available') AND version = ?`,
			formatTime(order.CreatedAt), vehicle.ID, vehicle.Version)
		if err != nil {
			return mapSQLError(err, "maintenance vehicle")
		}
		if err := rowsAffectedExactlyOne(vehicleResult, "maintenance vehicle"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO maintenance_orders(id, vehicle_id, status, reason, priority,
				previous_vehicle_status, assigned_technician, required_checks, completed_checks,
				resolution, version, created_by, created_at, started_at, completed_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			order.ID, order.VehicleID, order.Status, order.Reason, order.Priority,
			order.PreviousVehicleStatus, order.AssignedTechnician, required, completed,
			order.Resolution, order.Version, order.CreatedBy, formatTime(order.CreatedAt),
			nullableTime(order.StartedAt), nullableTime(order.CompletedAt)); err != nil {
			return mapSQLError(err, "maintenance order")
		}
		return insertAudit(ctx, tx, event)
	})
}

const maintenanceSelect = `SELECT id, vehicle_id, status, reason, priority, previous_vehicle_status,
	assigned_technician, required_checks, completed_checks, resolution, version, created_by,
	created_at, started_at, completed_at FROM maintenance_orders`

func (s *Store) MaintenanceByID(ctx context.Context, id string) (maintenance.WorkOrder, error) {
	var value maintenance.WorkOrder
	var status, required, completed, created string
	var started, finished sql.NullString
	err := s.db.QueryRowContext(ctx, maintenanceSelect+" WHERE id = ?", id).Scan(
		&value.ID, &value.VehicleID, &status, &value.Reason, &value.Priority,
		&value.PreviousVehicleStatus, &value.AssignedTechnician, &required, &completed,
		&value.Resolution, &value.Version, &value.CreatedBy, &created, &started, &finished)
	if err != nil {
		return maintenance.WorkOrder{}, mapSQLError(err, "maintenance order")
	}
	value.Status = maintenance.Status(status)
	if value.RequiredChecks, err = decodeStrings(required); err != nil {
		return maintenance.WorkOrder{}, err
	}
	if value.CompletedChecks, err = decodeStrings(completed); err != nil {
		return maintenance.WorkOrder{}, err
	}
	if value.CreatedAt, err = parseTime(created); err != nil {
		return maintenance.WorkOrder{}, err
	}
	if value.StartedAt, err = parseNullableTime(started); err != nil {
		return maintenance.WorkOrder{}, err
	}
	if value.CompletedAt, err = parseNullableTime(finished); err != nil {
		return maintenance.WorkOrder{}, err
	}
	return value, nil
}

func (s *Store) UpdateMaintenance(ctx context.Context, order maintenance.WorkOrder, expectedVersion int64) error {
	checks, err := encodeStrings(order.CompletedChecks)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE maintenance_orders SET status = ?, assigned_technician = ?, completed_checks = ?,
			resolution = ?, version = ?, started_at = ?, completed_at = ?
		WHERE id = ? AND version = ?`, order.Status, order.AssignedTechnician, checks,
		order.Resolution, order.Version, nullableTime(order.StartedAt), nullableTime(order.CompletedAt),
		order.ID, expectedVersion)
	if err != nil {
		return mapSQLError(err, "maintenance order")
	}
	return rowsAffectedExactlyOne(result, "maintenance order")
}

func (s *Store) CompleteMaintenance(ctx context.Context, order maintenance.WorkOrder, vehicle fleet.Vehicle, restore fleet.VehicleStatus, at time.Time, event audit.Event) error {
	checks, err := encodeStrings(order.CompletedChecks)
	if err != nil {
		return err
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		orderResult, err := tx.ExecContext(ctx, `
			UPDATE maintenance_orders SET status = 'completed', completed_checks = ?, resolution = ?,
				version = ?, completed_at = ? WHERE id = ? AND status = 'in_progress' AND version = ?`,
			checks, order.Resolution, order.Version, nullableTime(order.CompletedAt), order.ID, order.Version-1)
		if err != nil {
			return mapSQLError(err, "maintenance completion")
		}
		if err := rowsAffectedExactlyOne(orderResult, "maintenance completion"); err != nil {
			return err
		}
		vehicleResult, err := tx.ExecContext(ctx, `
			UPDATE vehicles SET status = ?, version = version + 1, updated_at = ?
			WHERE id = ? AND status = 'maintenance' AND version = ?`,
			restore, formatTime(at), vehicle.ID, vehicle.Version)
		if err != nil {
			return mapSQLError(err, "maintenance vehicle release")
		}
		if err := rowsAffectedExactlyOne(vehicleResult, "maintenance vehicle release"); err != nil {
			return err
		}
		return insertAudit(ctx, tx, event)
	})
}

func (s *Store) ActiveMaintenanceForVehicle(ctx context.Context, vehicleID string) (maintenance.WorkOrder, error) {
	row := s.db.QueryRowContext(ctx, maintenanceSelect+`
		WHERE vehicle_id = ? AND status IN ('open', 'in_progress', 'blocked')
		ORDER BY created_at DESC LIMIT 1`, vehicleID)
	var value maintenance.WorkOrder
	var status, required, completed, created string
	var started, finished sql.NullString
	err := row.Scan(&value.ID, &value.VehicleID, &status, &value.Reason, &value.Priority,
		&value.PreviousVehicleStatus, &value.AssignedTechnician, &required, &completed,
		&value.Resolution, &value.Version, &value.CreatedBy, &created, &started, &finished)
	if err != nil {
		return maintenance.WorkOrder{}, mapSQLError(err, "maintenance order")
	}
	value.Status = maintenance.Status(status)
	value.RequiredChecks, err = decodeStrings(required)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	value.CompletedChecks, err = decodeStrings(completed)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	value.CreatedAt, err = parseTime(created)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	value.StartedAt, err = parseNullableTime(started)
	if err != nil {
		return maintenance.WorkOrder{}, err
	}
	value.CompletedAt, err = parseNullableTime(finished)
	return value, err
}

func (s *Store) MaintenanceCounts(ctx context.Context) (map[maintenance.Status]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM maintenance_orders GROUP BY status`)
	if err != nil {
		return nil, mapSQLError(err, "maintenance counts")
	}
	defer rows.Close()
	counts := make(map[maintenance.Status]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, mapSQLError(err, "maintenance counts")
		}
		counts[maintenance.Status(status)] = count
	}
	return counts, rows.Err()
}
