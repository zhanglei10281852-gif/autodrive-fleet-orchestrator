CREATE TABLE service_readiness (
    component TEXT PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('ready', 'degraded', 'blocked')),
    message TEXT NOT NULL DEFAULT '',
    checked_at TEXT NOT NULL
);

CREATE TABLE idempotency_records (
    scope TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    response_code INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    PRIMARY KEY(scope, actor_id, idempotency_key)
);
CREATE INDEX idx_idempotency_expiry ON idempotency_records(expires_at);

CREATE TRIGGER prevent_trip_vehicle_overlap_insert
BEFORE INSERT ON trips
WHEN NEW.status IN ('scheduled', 'running')
AND EXISTS (
    SELECT 1 FROM trips
    WHERE vehicle_id = NEW.vehicle_id
      AND status IN ('scheduled', 'running')
)
BEGIN
    SELECT RAISE(ABORT, 'vehicle already has an active trip');
END;

CREATE TRIGGER prevent_trip_vehicle_overlap_update
BEFORE UPDATE OF status, vehicle_id ON trips
WHEN NEW.status IN ('scheduled', 'running')
AND EXISTS (
    SELECT 1 FROM trips
    WHERE vehicle_id = NEW.vehicle_id
      AND status IN ('scheduled', 'running')
      AND id <> NEW.id
)
BEGIN
    SELECT RAISE(ABORT, 'vehicle already has an active trip');
END;
