package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/maintenance"
)

func persistMaintenanceCompletion(ctx context.Context, tx *sql.Tx, order maintenance.WorkOrder, vehicle fleet.Vehicle, restore fleet.VehicleStatus, at time.Time, checks string) error {
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
	return rowsAffectedExactlyOne(vehicleResult, "maintenance vehicle release")
}
