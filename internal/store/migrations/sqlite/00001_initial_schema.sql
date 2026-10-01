-- +goose Up
CREATE TABLE scheduled_changes (
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    zone TEXT NOT NULL,
    status TEXT NOT NULL,
    scheduled_at TEXT,
    not_valid_after TEXT,
    auto_prerequisites INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    created_by TEXT,
    updated_at TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT,
    last_error TEXT,
    applied_at TEXT,
    result_rcode TEXT,
    new_serial INTEGER,
    reverted_at TEXT,
    lease_owner TEXT,
    lease_expires_at TEXT,
    source TEXT NOT NULL DEFAULT 'scheduler',
    PRIMARY KEY (id)
);

CREATE INDEX idx_scheduled_changes_status ON scheduled_changes (status);
CREATE INDEX idx_scheduled_changes_zone ON scheduled_changes (zone);
CREATE INDEX idx_scheduled_changes_due ON scheduled_changes (status, scheduled_at);
CREATE INDEX idx_scheduled_changes_source ON scheduled_changes (source);
CREATE INDEX idx_scheduled_changes_retention ON scheduled_changes (status, updated_at);

CREATE TABLE scheduled_operations (
    change_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    action TEXT NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    rdclass TEXT NOT NULL DEFAULT 'IN',
    ttl INTEGER NOT NULL DEFAULT 3600,
    records TEXT,
    prior_ttl INTEGER,
    prior_records TEXT,
    snapshot_at TEXT,
    PRIMARY KEY (change_id, seq),
    FOREIGN KEY (change_id) REFERENCES scheduled_changes (id) ON DELETE CASCADE
);

CREATE TABLE scheduled_prerequisites (
    change_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    prereq_type TEXT NOT NULL,
    name TEXT NOT NULL,
    rdtype TEXT,
    rdclass TEXT NOT NULL DEFAULT 'IN',
    data TEXT,
    PRIMARY KEY (change_id, seq),
    FOREIGN KEY (change_id) REFERENCES scheduled_changes (id) ON DELETE CASCADE
);

CREATE TABLE scheduled_change_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    change_id TEXT NOT NULL,
    ts TEXT NOT NULL,
    event TEXT NOT NULL,
    actor TEXT,
    detail TEXT,
    FOREIGN KEY (change_id) REFERENCES scheduled_changes (id) ON DELETE CASCADE
);

CREATE INDEX idx_scheduled_change_events_change ON scheduled_change_events (change_id);
CREATE INDEX idx_scheduled_change_events_event ON scheduled_change_events (event);
CREATE INDEX idx_scheduled_change_events_actor ON scheduled_change_events (actor);
CREATE INDEX idx_scheduled_change_events_ts ON scheduled_change_events (ts DESC);

-- +goose Down
DROP TABLE IF EXISTS scheduled_change_events;
DROP TABLE IF EXISTS scheduled_prerequisites;
DROP TABLE IF EXISTS scheduled_operations;
DROP TABLE IF EXISTS scheduled_changes;
