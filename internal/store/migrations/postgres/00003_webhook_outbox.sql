-- +goose Up
CREATE TABLE webhook_outbox (
    id TEXT NOT NULL,
    event_json JSONB NOT NULL,
    target TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ,
    last_error TEXT,
    CONSTRAINT pk_webhook_outbox PRIMARY KEY (id)
);

CREATE INDEX idx_webhook_outbox_drain ON webhook_outbox (delivered_at, next_attempt_at);

-- +goose Down
DROP TABLE IF EXISTS webhook_outbox;
