-- +goose Up
CREATE TABLE webhook_outbox (
    id TEXT NOT NULL,
    event_json TEXT NOT NULL,
    target TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT,
    created_at TEXT NOT NULL,
    delivered_at TEXT,
    last_error TEXT,
    PRIMARY KEY (id)
);

CREATE INDEX idx_webhook_outbox_drain ON webhook_outbox (delivered_at, next_attempt_at);

-- +goose Down
DROP TABLE IF EXISTS webhook_outbox;
