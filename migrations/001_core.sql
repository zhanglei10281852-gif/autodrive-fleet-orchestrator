CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('dispatcher', 'safety_operator', 'fleet_admin')),
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_user_active ON sessions(user_id, expires_at, revoked_at);

CREATE TABLE regions (
    id TEXT PRIMARY KEY,
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    timezone TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('draft', 'active', 'paused', 'retired')),
    max_vehicles INTEGER NOT NULL CHECK (max_vehicles > 0),
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE vehicles (
    id TEXT PRIMARY KEY,
    region_id TEXT NOT NULL REFERENCES regions(id),
    vin TEXT NOT NULL UNIQUE,
    fleet_number TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('draft', 'offline', 'available', 'reserved', 'in_trip', 'charging', 'maintenance', 'suspended')),
    capability TEXT NOT NULL,
    battery_percent INTEGER NOT NULL CHECK (battery_percent BETWEEN 0 AND 100),
    latitude REAL NOT NULL DEFAULT 0,
    longitude REAL NOT NULL DEFAULT 0,
    last_telemetry_at TEXT,
    safety_valid_until TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_vehicles_dispatch ON vehicles(region_id, status, capability, battery_percent);

CREATE TABLE missions (
    id TEXT PRIMARY KEY,
    region_id TEXT NOT NULL REFERENCES regions(id),
    external_reference TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    kind TEXT NOT NULL,
    priority TEXT NOT NULL CHECK (priority IN ('routine', 'urgent', 'critical')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'assigned', 'in_progress', 'completed', 'cancelled', 'failed')),
    pickup_latitude REAL NOT NULL,
    pickup_longitude REAL NOT NULL,
    dropoff_latitude REAL NOT NULL,
    dropoff_longitude REAL NOT NULL,
    earliest_start_at TEXT NOT NULL,
    deadline_at TEXT NOT NULL,
    minimum_battery INTEGER NOT NULL,
    required_capability TEXT NOT NULL,
    assigned_vehicle_id TEXT REFERENCES vehicles(id),
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(created_by, idempotency_key)
);
CREATE INDEX idx_missions_dispatch ON missions(region_id, status, priority, deadline_at);

CREATE TABLE trips (
    id TEXT PRIMARY KEY,
    mission_id TEXT NOT NULL UNIQUE REFERENCES missions(id),
    vehicle_id TEXT NOT NULL REFERENCES vehicles(id),
    status TEXT NOT NULL CHECK (status IN ('scheduled', 'running', 'completed', 'aborted')),
    scheduled_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    abort_reason TEXT NOT NULL DEFAULT '',
    distance_meters INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_trips_vehicle_status ON trips(vehicle_id, status, scheduled_at);

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL,
    actor_role TEXT NOT NULL,
    action TEXT NOT NULL,
    object_type TEXT NOT NULL,
    object_id TEXT NOT NULL,
    result TEXT NOT NULL CHECK (result IN ('success', 'denied', 'failure')),
    request_id TEXT NOT NULL,
    details TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX idx_audit_query ON audit_events(object_type, object_id, action, created_at DESC);
CREATE INDEX idx_audit_actor ON audit_events(actor_id, created_at DESC);

CREATE TABLE outbox_events (
    id TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    payload TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'leased', 'completed', 'dead')),
    attempt INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    available_at TEXT NOT NULL,
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_until TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_outbox_claim ON outbox_events(status, available_at, lease_until);
