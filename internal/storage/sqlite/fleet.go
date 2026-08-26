package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/fleet"
)

func (s *Store) CreateRegion(ctx context.Context, region fleet.Region) error {
	if err := region.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO regions(id, code, name, timezone, status, max_vehicles, version, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		region.ID, region.Code, region.Name, region.Timezone, region.Status, region.MaxVehicles,
		region.Version, formatTime(region.CreatedAt), formatTime(region.UpdatedAt))
	return mapSQLError(err, "region")
}

func (s *Store) RegionByID(ctx context.Context, id string) (fleet.Region, error) {
	return scanRegion(s.db.QueryRowContext(ctx, `
		SELECT id, code, name, timezone, status, max_vehicles, version, created_at, updated_at
		FROM regions WHERE id = ?`, id))
}

func scanRegion(row *sql.Row) (fleet.Region, error) {
	var region fleet.Region
	var status, created, updated string
	if err := row.Scan(&region.ID, &region.Code, &region.Name, &region.Timezone, &status,
		&region.MaxVehicles, &region.Version, &created, &updated); err != nil {
		return fleet.Region{}, mapSQLError(err, "region")
	}
	region.Status = fleet.RegionStatus(status)
	var err error
	if region.CreatedAt, err = parseTime(created); err != nil {
		return fleet.Region{}, err
	}
	if region.UpdatedAt, err = parseTime(updated); err != nil {
		return fleet.Region{}, err
	}
	return region, nil
}

func (s *Store) UpdateRegion(ctx context.Context, region fleet.Region, expectedVersion int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE regions SET name = ?, timezone = ?, status = ?, max_vehicles = ?, version = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		region.Name, region.Timezone, region.Status, region.MaxVehicles, region.Version,
		formatTime(region.UpdatedAt), region.ID, expectedVersion)
	if err != nil {
		return mapSQLError(err, "region")
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return common.ConflictError{Resource: "region", Reason: "version changed or region does not exist"}
	}
	return nil
}

func (s *Store) RegionVehicleCount(ctx context.Context, regionID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vehicles WHERE region_id = ?`, regionID).Scan(&count); err != nil {
		return 0, mapSQLError(err, "region vehicle count")
	}
	return count, nil
}

func (s *Store) CreateVehicle(ctx context.Context, vehicle fleet.Vehicle) error {
	if err := vehicle.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vehicles(
			id, region_id, vin, fleet_number, status, capability, battery_percent,
			latitude, longitude, last_telemetry_at, safety_valid_until, version, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		vehicle.ID, vehicle.RegionID, vehicle.VIN, vehicle.FleetNumber, vehicle.Status,
		vehicle.Capability, vehicle.BatteryPercent, vehicle.Latitude, vehicle.Longitude,
		nullableTime(vehicle.LastTelemetryAt), nullableTime(vehicle.SafetyValidUntil), vehicle.Version,
		formatTime(vehicle.CreatedAt), formatTime(vehicle.UpdatedAt))
	return mapSQLError(err, "vehicle")
}

func (s *Store) VehicleByID(ctx context.Context, id string) (fleet.Vehicle, error) {
	return scanVehicle(s.db.QueryRowContext(ctx, vehicleSelect+" WHERE id = ?", id))
}

func (s *Store) VehicleByFleetNumber(ctx context.Context, number string) (fleet.Vehicle, error) {
	return scanVehicle(s.db.QueryRowContext(ctx, vehicleSelect+" WHERE fleet_number = ?", number))
}

const vehicleSelect = `SELECT id, region_id, vin, fleet_number, status, capability, battery_percent,
	latitude, longitude, last_telemetry_at, safety_valid_until, version, created_at, updated_at FROM vehicles`

func scanVehicle(row *sql.Row) (fleet.Vehicle, error) {
	var vehicle fleet.Vehicle
	var status, created, updated string
	var telemetry, safety sql.NullString
	if err := row.Scan(
		&vehicle.ID, &vehicle.RegionID, &vehicle.VIN, &vehicle.FleetNumber, &status,
		&vehicle.Capability, &vehicle.BatteryPercent, &vehicle.Latitude, &vehicle.Longitude,
		&telemetry, &safety, &vehicle.Version, &created, &updated,
	); err != nil {
		return fleet.Vehicle{}, mapSQLError(err, "vehicle")
	}
	vehicle.Status = fleet.VehicleStatus(status)
	var err error
	if vehicle.LastTelemetryAt, err = parseNullableTime(telemetry); err != nil {
		return fleet.Vehicle{}, err
	}
	if vehicle.SafetyValidUntil, err = parseNullableTime(safety); err != nil {
		return fleet.Vehicle{}, err
	}
	if vehicle.CreatedAt, err = parseTime(created); err != nil {
		return fleet.Vehicle{}, err
	}
	if vehicle.UpdatedAt, err = parseTime(updated); err != nil {
		return fleet.Vehicle{}, err
	}
	return vehicle, nil
}

func (s *Store) ListVehicles(ctx context.Context, filter fleet.Filter) (common.Page[fleet.Vehicle], error) {
	page := filter.Page.Normalize()
	conditions := []string{"1 = 1"}
	args := make([]any, 0, 8)
	if filter.RegionID != "" {
		conditions = append(conditions, "region_id = ?")
		args = append(args, filter.RegionID)
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.Capability != "" {
		conditions = append(conditions, "capability = ?")
		args = append(args, filter.Capability)
	}
	if filter.MinBattery > 0 {
		conditions = append(conditions, "battery_percent >= ?")
		args = append(args, filter.MinBattery)
	}
	if filter.Search != "" {
		conditions = append(conditions, "(vin LIKE ? OR fleet_number LIKE ?)")
		term := "%" + strings.TrimSpace(filter.Search) + "%"
		args = append(args, term, term)
	}
	where := strings.Join(conditions, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM vehicles WHERE "+where, args...).Scan(&total); err != nil {
		return common.Page[fleet.Vehicle]{}, mapSQLError(err, "vehicle list count")
	}
	queryArgs := append(append([]any(nil), args...), page.Limit, page.Offset)
	rows, err := s.db.QueryContext(ctx, vehicleSelect+" WHERE "+where+" ORDER BY fleet_number ASC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return common.Page[fleet.Vehicle]{}, mapSQLError(err, "vehicle list")
	}
	defer rows.Close()
	vehicles := make([]fleet.Vehicle, 0, page.Limit)
	for rows.Next() {
		vehicle, err := scanVehicleRows(rows)
		if err != nil {
			return common.Page[fleet.Vehicle]{}, err
		}
		vehicles = append(vehicles, vehicle)
	}
	if err := rows.Err(); err != nil {
		return common.Page[fleet.Vehicle]{}, mapSQLError(err, "vehicle list")
	}
	return common.NewPage(vehicles, total, page), nil
}

func scanVehicleRows(rows *sql.Rows) (fleet.Vehicle, error) {
	var vehicle fleet.Vehicle
	var status, created, updated string
	var telemetry, safety sql.NullString
	if err := rows.Scan(
		&vehicle.ID, &vehicle.RegionID, &vehicle.VIN, &vehicle.FleetNumber, &status,
		&vehicle.Capability, &vehicle.BatteryPercent, &vehicle.Latitude, &vehicle.Longitude,
		&telemetry, &safety, &vehicle.Version, &created, &updated,
	); err != nil {
		return fleet.Vehicle{}, mapSQLError(err, "vehicle")
	}
	vehicle.Status = fleet.VehicleStatus(status)
	var err error
	vehicle.LastTelemetryAt, err = parseNullableTime(telemetry)
	if err != nil {
		return fleet.Vehicle{}, err
	}
	vehicle.SafetyValidUntil, err = parseNullableTime(safety)
	if err != nil {
		return fleet.Vehicle{}, err
	}
	vehicle.CreatedAt, err = parseTime(created)
	if err != nil {
		return fleet.Vehicle{}, err
	}
	vehicle.UpdatedAt, err = parseTime(updated)
	return vehicle, err
}

func (s *Store) UpdateVehicle(ctx context.Context, vehicle fleet.Vehicle, expectedVersion int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE vehicles SET region_id = ?, status = ?, capability = ?, battery_percent = ?,
			latitude = ?, longitude = ?, last_telemetry_at = ?, safety_valid_until = ?,
			version = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		vehicle.RegionID, vehicle.Status, vehicle.Capability, vehicle.BatteryPercent,
		vehicle.Latitude, vehicle.Longitude, nullableTime(vehicle.LastTelemetryAt),
		nullableTime(vehicle.SafetyValidUntil), vehicle.Version, formatTime(vehicle.UpdatedAt),
		vehicle.ID, expectedVersion)
	if err != nil {
		return mapSQLError(err, "vehicle")
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return common.ConflictError{Resource: "vehicle", Reason: "version changed or vehicle does not exist"}
	}
	return nil
}

func updateVehicleStatus(ctx context.Context, q Queryer, id string, from, to fleet.VehicleStatus, expectedVersion int64, at time.Time) error {
	result, err := q.ExecContext(ctx, `
		UPDATE vehicles SET status = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND status = ? AND version = ?`, to, formatTime(at), id, from, expectedVersion)
	if err != nil {
		return mapSQLError(err, "vehicle state")
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return common.ConflictError{Resource: "vehicle", Reason: "status or version changed"}
	}
	return nil
}

func (s *Store) SetVehicleSafetyValidity(ctx context.Context, id string, validUntil, at time.Time, expectedVersion int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE vehicles SET safety_valid_until = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, formatTime(validUntil), formatTime(at), id, expectedVersion)
	if err != nil {
		return mapSQLError(err, "vehicle safety")
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return common.ErrConflict
	}
	return nil
}

func (s *Store) AvailableVehicleCandidates(ctx context.Context, regionID, capability string, minimumBattery int, now time.Time, limit int) ([]fleet.Vehicle, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, vehicleSelect+`
		WHERE region_id = ? AND status = 'available' AND capability = ?
		  AND battery_percent >= ? AND safety_valid_until > ?
		ORDER BY battery_percent DESC, last_telemetry_at DESC, fleet_number ASC LIMIT ?`,
		regionID, capability, minimumBattery, formatTime(now), limit)
	if err != nil {
		return nil, mapSQLError(err, "dispatch candidates")
	}
	defer rows.Close()
	result := make([]fleet.Vehicle, 0, limit)
	for rows.Next() {
		vehicle, err := scanVehicleRows(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, vehicle)
	}
	return result, rows.Err()
}

func (s *Store) VehicleActiveTripCount(ctx context.Context, vehicleID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM trips WHERE vehicle_id = ? AND status IN ('scheduled', 'running')`, vehicleID).Scan(&count); err != nil {
		return 0, mapSQLError(err, "active trip count")
	}
	return count, nil
}

func (s *Store) AssertVehicleRegion(ctx context.Context, vehicleID, regionID string) error {
	var found string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM vehicles WHERE id = ? AND region_id = ?`, vehicleID, regionID).Scan(&found)
	if err != nil {
		return mapSQLError(err, "vehicle region")
	}
	return nil
}

func rowsAffectedExactlyOne(result sql.Result, resource string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s update result: %w", resource, err)
	}
	if changed != 1 {
		return common.ConflictError{Resource: resource, Reason: "concurrent state change"}
	}
	return nil
}
