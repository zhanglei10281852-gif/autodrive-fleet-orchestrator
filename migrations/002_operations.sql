CREATE TABLE telemetry_samples (
    event_id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL REFERENCES vehicles(id),
    observed_at TEXT NOT NULL,
    latitude REAL NOT NULL,
    longitude REAL NOT NULL,
    speed_kph REAL NOT NULL,
    battery_percent INTEGER NOT NULL,
    odometer_meters INTEGER NOT NULL,
    fault_code TEXT NOT NULL DEFAULT '',
    severity TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    payload_hash TEXT NOT NULL,
    received_at TEXT NOT NULL
);
CREATE INDEX idx_telemetry_vehicle_time ON telemetry_samples(vehicle_id, observed_at DESC);

CREATE TABLE safety_incidents (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL REFERENCES vehicles(id),
    telemetry_event_id TEXT REFERENCES telemetry_samples(event_id),
    severity TEXT NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    category TEXT NOT NULL,
    summary TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open', 'acknowledged', 'mitigating', 'resolved', 'closed')),
    owner_id TEXT REFERENCES users(id),
    lease_until TEXT,
    resolution TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    opened_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    closed_at TEXT
);
CREATE INDEX idx_incidents_queue ON safety_incidents(status, severity, opened_at);
CREATE INDEX idx_incidents_vehicle ON safety_incidents(vehicle_id, status);

CREATE TABLE charging_stations (
    id TEXT PRIMARY KEY,
    region_id TEXT NOT NULL REFERENCES regions(id),
    code TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE charging_connectors (
    id TEXT PRIMARY KEY,
    station_id TEXT NOT NULL REFERENCES charging_stations(id),
    code TEXT NOT NULL,
    power_kw INTEGER NOT NULL,
    active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    version INTEGER NOT NULL DEFAULT 1,
    lease_owner_id TEXT NOT NULL DEFAULT '',
    lease_until TEXT,
    UNIQUE(station_id, code)
);

CREATE TABLE charging_sessions (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL REFERENCES vehicles(id),
    connector_id TEXT NOT NULL REFERENCES charging_connectors(id),
    status TEXT NOT NULL CHECK (status IN ('reserved', 'active', 'completed', 'cancelled', 'expired')),
    window_start TEXT NOT NULL,
    window_end TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    initial_battery INTEGER NOT NULL,
    final_battery INTEGER,
    energy_watt_hours INTEGER NOT NULL DEFAULT 0,
    idempotency_key TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    UNIQUE(created_by, idempotency_key)
);
CREATE INDEX idx_charging_conflict ON charging_sessions(connector_id, status, window_start, window_end);
CREATE INDEX idx_charging_vehicle ON charging_sessions(vehicle_id, status);

CREATE TABLE maintenance_orders (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL REFERENCES vehicles(id),
    status TEXT NOT NULL CHECK (status IN ('open', 'in_progress', 'blocked', 'completed', 'cancelled')),
    reason TEXT NOT NULL,
    priority TEXT NOT NULL,
    previous_vehicle_status TEXT NOT NULL,
    assigned_technician TEXT NOT NULL DEFAULT '',
    required_checks TEXT NOT NULL,
    completed_checks TEXT NOT NULL DEFAULT '[]',
    resolution TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT
);
CREATE UNIQUE INDEX idx_maintenance_active_vehicle
ON maintenance_orders(vehicle_id)
WHERE status IN ('open', 'in_progress', 'blocked');
