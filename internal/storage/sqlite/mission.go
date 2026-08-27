package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/audit"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/common"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/mission"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/domain/trip"
	"github.com/zhanglei10281852-gif/autodrive-fleet-orchestrator/internal/repository"
)

func (s *Store) CreateMission(ctx context.Context, value mission.Mission) error {
	_, err := s.db.ExecContext(ctx, missionInsert,
		value.ID, value.RegionID, value.ExternalReference, value.IdempotencyKey, value.Kind,
		value.Priority, value.Status, value.PickupLatitude, value.PickupLongitude,
		value.DropoffLatitude, value.DropoffLongitude, formatTime(value.EarliestStartAt),
		formatTime(value.DeadlineAt), value.MinimumBattery, value.RequiredCapability,
		nullableString(value.AssignedVehicleID), value.Version, value.CreatedBy,
		formatTime(value.CreatedAt), formatTime(value.UpdatedAt))
	return mapSQLError(err, "mission")
}

const missionInsert = `INSERT INTO missions(
	id, region_id, external_reference, idempotency_key, kind, priority, status,
	pickup_latitude, pickup_longitude, dropoff_latitude, dropoff_longitude,
	earliest_start_at, deadline_at, minimum_battery, required_capability,
	assigned_vehicle_id, version, created_by, created_at, updated_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const missionSelect = `SELECT id, region_id, external_reference, idempotency_key, kind, priority, status,
	pickup_latitude, pickup_longitude, dropoff_latitude, dropoff_longitude,
	earliest_start_at, deadline_at, minimum_battery, required_capability,
	assigned_vehicle_id, version, created_by, created_at, updated_at FROM missions`

func (s *Store) MissionByID(ctx context.Context, id string) (mission.Mission, error) {
	return scanMission(s.db.QueryRowContext(ctx, missionSelect+" WHERE id = ?", id))
}

func (s *Store) MissionByIdempotency(ctx context.Context, actorID, key string) (mission.Mission, error) {
	return scanMission(s.db.QueryRowContext(ctx, missionSelect+" WHERE created_by = ? AND idempotency_key = ?", actorID, key))
}

func scanMission(row *sql.Row) (mission.Mission, error) {
	var value mission.Mission
	var priority, status, earliest, deadline, created, updated string
	var assigned sql.NullString
	if err := row.Scan(
		&value.ID, &value.RegionID, &value.ExternalReference, &value.IdempotencyKey,
		&value.Kind, &priority, &status, &value.PickupLatitude, &value.PickupLongitude,
		&value.DropoffLatitude, &value.DropoffLongitude, &earliest, &deadline,
		&value.MinimumBattery, &value.RequiredCapability, &assigned, &value.Version,
		&value.CreatedBy, &created, &updated,
	); err != nil {
		return mission.Mission{}, mapSQLError(err, "mission")
	}
	value.Priority = mission.Priority(priority)
	value.Status = mission.Status(status)
	value.AssignedVehicleID = assigned.String
	var err error
	if value.EarliestStartAt, err = parseTime(earliest); err != nil {
		return mission.Mission{}, err
	}
	if value.DeadlineAt, err = parseTime(deadline); err != nil {
		return mission.Mission{}, err
	}
	if value.CreatedAt, err = parseTime(created); err != nil {
		return mission.Mission{}, err
	}
	if value.UpdatedAt, err = parseTime(updated); err != nil {
		return mission.Mission{}, err
	}
	return value, nil
}

func (s *Store) ListMissions(ctx context.Context, filter mission.Filter) (common.Page[mission.Mission], error) {
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
	if filter.Priority != "" {
		conditions = append(conditions, "priority = ?")
		args = append(args, filter.Priority)
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
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM missions WHERE "+where, args...).Scan(&total); err != nil {
		return common.Page[mission.Mission]{}, mapSQLError(err, "mission count")
	}
	queryArgs := append(append([]any(nil), args...), page.Limit, page.Offset)
	rows, err := s.db.QueryContext(ctx, missionSelect+" WHERE "+where+" ORDER BY deadline_at, priority DESC LIMIT ? OFFSET ?", queryArgs...)
	if err != nil {
		return common.Page[mission.Mission]{}, mapSQLError(err, "mission list")
	}
	defer rows.Close()
	values := make([]mission.Mission, 0, page.Limit)
	for rows.Next() {
		value, err := scanMissionRows(rows)
		if err != nil {
			return common.Page[mission.Mission]{}, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return common.Page[mission.Mission]{}, mapSQLError(err, "mission list")
	}
	return common.NewPage(values, total, page), nil
}

func scanMissionRows(rows *sql.Rows) (mission.Mission, error) {
	var value mission.Mission
	var priority, status, earliest, deadline, created, updated string
	var assigned sql.NullString
	if err := rows.Scan(
		&value.ID, &value.RegionID, &value.ExternalReference, &value.IdempotencyKey,
		&value.Kind, &priority, &status, &value.PickupLatitude, &value.PickupLongitude,
		&value.DropoffLatitude, &value.DropoffLongitude, &earliest, &deadline,
		&value.MinimumBattery, &value.RequiredCapability, &assigned, &value.Version,
		&value.CreatedBy, &created, &updated,
	); err != nil {
		return mission.Mission{}, mapSQLError(err, "mission")
	}
	value.Priority = mission.Priority(priority)
	value.Status = mission.Status(status)
	value.AssignedVehicleID = assigned.String
	var err error
	value.EarliestStartAt, err = parseTime(earliest)
	if err != nil {
		return mission.Mission{}, err
	}
	value.DeadlineAt, err = parseTime(deadline)
	if err != nil {
		return mission.Mission{}, err
	}
	value.CreatedAt, err = parseTime(created)
	if err != nil {
		return mission.Mission{}, err
	}
	value.UpdatedAt, err = parseTime(updated)
	return value, err
}

func (s *Store) CommitDispatch(ctx context.Context, commit repository.DispatchCommit) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		missionResult, err := tx.ExecContext(ctx, `
			UPDATE missions SET status = ?, assigned_vehicle_id = ?, version = ?, updated_at = ?
			WHERE id = ? AND status = 'pending' AND version = ?`,
			commit.Mission.Status, commit.Mission.AssignedVehicleID, commit.Mission.Version,
			formatTime(commit.Mission.UpdatedAt), commit.Mission.ID, commit.ExpectedMissionVersion)
		if err != nil {
			return mapSQLError(err, "mission dispatch")
		}
		if err := rowsAffectedExactlyOne(missionResult, "mission dispatch"); err != nil {
			return err
		}
		vehicleResult, err := tx.ExecContext(ctx, `
			UPDATE vehicles SET status = 'reserved', version = ?, updated_at = ?
			WHERE id = ? AND status = 'available' AND version = ?`,
			commit.Vehicle.Version, formatTime(commit.Vehicle.UpdatedAt), commit.Vehicle.ID, commit.ExpectedVehicleVersion)
		if err != nil {
			return mapSQLError(err, "vehicle reservation")
		}
		if err := rowsAffectedExactlyOne(vehicleResult, "vehicle reservation"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO trips(id, mission_id, vehicle_id, status, scheduled_at, started_at, completed_at,
				abort_reason, distance_meters, version, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, NULL, NULL, '', 0, ?, ?, ?)`,
			commit.Trip.ID, commit.Trip.MissionID, commit.Trip.VehicleID, commit.Trip.Status,
			formatTime(commit.Trip.ScheduledAt), commit.Trip.Version,
			formatTime(commit.Trip.CreatedAt), formatTime(commit.Trip.UpdatedAt)); err != nil {
			return mapSQLError(err, "trip")
		}
		return insertAudit(ctx, tx, commit.Audit)
	})
}

func (s *Store) AssignMissionForDispatch(ctx context.Context, commit repository.DispatchCommit) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE missions SET status = ?, assigned_vehicle_id = ?, version = ?, updated_at = ?
		WHERE id = ? AND status = 'pending' AND version = ?`,
		commit.Mission.Status, commit.Mission.AssignedVehicleID, commit.Mission.Version,
		formatTime(commit.Mission.UpdatedAt), commit.Mission.ID, commit.ExpectedMissionVersion)
	if err != nil {
		return mapSQLError(err, "mission dispatch")
	}
	return rowsAffectedExactlyOne(result, "mission dispatch")
}

func (s *Store) CommitDispatchResources(ctx context.Context, commit repository.DispatchCommit) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		vehicleResult, err := tx.ExecContext(ctx, `
			UPDATE vehicles SET status = 'reserved', version = ?, updated_at = ?
			WHERE id = ? AND status = 'available' AND version = ?`,
			commit.Vehicle.Version, formatTime(commit.Vehicle.UpdatedAt), commit.Vehicle.ID, commit.ExpectedVehicleVersion)
		if err != nil {
			return mapSQLError(err, "vehicle reservation")
		}
		if err := rowsAffectedExactlyOne(vehicleResult, "vehicle reservation"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO trips(id, mission_id, vehicle_id, status, scheduled_at, started_at, completed_at,
				abort_reason, distance_meters, version, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, NULL, NULL, '', 0, ?, ?, ?)`,
			commit.Trip.ID, commit.Trip.MissionID, commit.Trip.VehicleID, commit.Trip.Status,
			formatTime(commit.Trip.ScheduledAt), commit.Trip.Version,
			formatTime(commit.Trip.CreatedAt), formatTime(commit.Trip.UpdatedAt)); err != nil {
			return mapSQLError(err, "trip")
		}
		return insertAudit(ctx, tx, commit.Audit)
	})
}

func (s *Store) TripByID(ctx context.Context, id string) (trip.Trip, error) {
	return scanTrip(s.db.QueryRowContext(ctx, tripSelect+" WHERE id = ?", id))
}

func (s *Store) TripByMissionID(ctx context.Context, missionID string) (trip.Trip, error) {
	return scanTrip(s.db.QueryRowContext(ctx, tripSelect+" WHERE mission_id = ?", missionID))
}

const tripSelect = `SELECT id, mission_id, vehicle_id, status, scheduled_at, started_at,
	completed_at, abort_reason, distance_meters, version, created_at, updated_at FROM trips`

func scanTrip(row *sql.Row) (trip.Trip, error) {
	var value trip.Trip
	var status, scheduled, created, updated string
	var started, completed sql.NullString
	if err := row.Scan(&value.ID, &value.MissionID, &value.VehicleID, &status, &scheduled,
		&started, &completed, &value.AbortReason, &value.DistanceMeters, &value.Version,
		&created, &updated); err != nil {
		return trip.Trip{}, mapSQLError(err, "trip")
	}
	value.Status = trip.Status(status)
	var err error
	if value.ScheduledAt, err = parseTime(scheduled); err != nil {
		return trip.Trip{}, err
	}
	if value.StartedAt, err = parseNullableTime(started); err != nil {
		return trip.Trip{}, err
	}
	if value.CompletedAt, err = parseNullableTime(completed); err != nil {
		return trip.Trip{}, err
	}
	if value.CreatedAt, err = parseTime(created); err != nil {
		return trip.Trip{}, err
	}
	if value.UpdatedAt, err = parseTime(updated); err != nil {
		return trip.Trip{}, err
	}
	return value, nil
}

func (s *Store) CommitTripStart(ctx context.Context, commit repository.TripStartCommit) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		tripResult, err := tx.ExecContext(ctx, `
			UPDATE trips SET status = 'running', started_at = ?, version = ?, updated_at = ?
			WHERE id = ? AND status = 'scheduled' AND version = ?`,
			nullableTime(commit.Trip.StartedAt), commit.Trip.Version, formatTime(commit.Trip.UpdatedAt),
			commit.Trip.ID, commit.ExpectedTripVersion)
		if err != nil {
			return mapSQLError(err, "trip start")
		}
		if err := rowsAffectedExactlyOne(tripResult, "trip start"); err != nil {
			return err
		}
		missionResult, err := tx.ExecContext(ctx, `
			UPDATE missions SET status = 'in_progress', version = ?, updated_at = ?
			WHERE id = ? AND status = 'assigned' AND version = ?`,
			commit.Mission.Version, formatTime(commit.Mission.UpdatedAt), commit.Mission.ID, commit.ExpectedMissionVersion)
		if err != nil {
			return mapSQLError(err, "mission start")
		}
		if err := rowsAffectedExactlyOne(missionResult, "mission start"); err != nil {
			return err
		}
		vehicleResult, err := tx.ExecContext(ctx, `
			UPDATE vehicles SET status = 'in_trip', version = ?, updated_at = ?
			WHERE id = ? AND status = 'reserved' AND version = ?`,
			commit.Vehicle.Version, formatTime(commit.Vehicle.UpdatedAt), commit.Vehicle.ID, commit.ExpectedVehicleVersion)
		if err != nil {
			return mapSQLError(err, "vehicle trip start")
		}
		if err := rowsAffectedExactlyOne(vehicleResult, "vehicle trip start"); err != nil {
			return err
		}
		return insertAudit(ctx, tx, commit.Audit)
	})
}

func (s *Store) CommitTripCompletion(ctx context.Context, commit repository.TripCompletionCommit) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		tripResult, err := tx.ExecContext(ctx, `
			UPDATE trips SET status = ?, completed_at = ?, distance_meters = ?, version = ?, updated_at = ?
			WHERE id = ? AND status = 'running' AND version = ?`,
			commit.Trip.Status, nullableTime(commit.Trip.CompletedAt), commit.Trip.DistanceMeters,
			commit.Trip.Version, formatTime(commit.Trip.UpdatedAt), commit.Trip.ID, commit.ExpectedTripVersion)
		if err != nil {
			return mapSQLError(err, "trip completion")
		}
		if err := rowsAffectedExactlyOne(tripResult, "trip completion"); err != nil {
			return err
		}
		missionResult, err := tx.ExecContext(ctx, `
			UPDATE missions SET status = ?, version = ?, updated_at = ?
			WHERE id = ? AND status = 'in_progress' AND version = ?`,
			commit.Mission.Status, commit.Mission.Version, formatTime(commit.Mission.UpdatedAt),
			commit.Mission.ID, commit.ExpectedMissionVersion)
		if err != nil {
			return mapSQLError(err, "mission completion")
		}
		if err := rowsAffectedExactlyOne(missionResult, "mission completion"); err != nil {
			return err
		}
		vehicleResult, err := tx.ExecContext(ctx, `
			UPDATE vehicles SET status = ?, version = ?, updated_at = ?
			WHERE id = ? AND status = 'in_trip' AND version = ?`,
			commit.Vehicle.Status, commit.Vehicle.Version, formatTime(commit.Vehicle.UpdatedAt),
			commit.Vehicle.ID, commit.ExpectedVehicleVersion)
		if err != nil {
			return mapSQLError(err, "vehicle release")
		}
		if err := rowsAffectedExactlyOne(vehicleResult, "vehicle release"); err != nil {
			return err
		}
		if err := insertAudit(ctx, tx, commit.Audit); err != nil {
			return err
		}
		return insertOutbox(ctx, tx, commit.Outbox)
	})
}

func (s *Store) CancelPendingMission(ctx context.Context, id string, expectedVersion int64, at time.Time, event audit.Event) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE missions SET status = 'cancelled', version = version + 1, updated_at = ?
			WHERE id = ? AND status = 'pending' AND version = ?`, formatTime(at), id, expectedVersion)
		if err != nil {
			return mapSQLError(err, "mission cancellation")
		}
		if err := rowsAffectedExactlyOne(result, "mission cancellation"); err != nil {
			return err
		}
		return insertAudit(ctx, tx, event)
	})
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Store) ActiveTripForVehicle(ctx context.Context, vehicleID string) (trip.Trip, error) {
	return scanTrip(s.db.QueryRowContext(ctx, tripSelect+`
		WHERE vehicle_id = ? AND status IN ('scheduled', 'running') ORDER BY created_at DESC LIMIT 1`, vehicleID))
}

func (s *Store) MissionCountsByStatus(ctx context.Context, regionID string) (map[mission.Status]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT status, COUNT(*) FROM missions WHERE region_id = ? GROUP BY status`, regionID)
	if err != nil {
		return nil, mapSQLError(err, "mission status counts")
	}
	defer rows.Close()
	counts := make(map[mission.Status]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan mission counts: %w", err)
		}
		counts[mission.Status(status)] = count
	}
	return counts, rows.Err()
}
