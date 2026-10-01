-- +goose Up
-- Present in 00001 for new installs; IF NOT EXISTS keeps upgrade parity with Python 0002.
CREATE INDEX IF NOT EXISTS idx_scheduled_changes_retention ON scheduled_changes (status, updated_at);

-- +goose Down
DROP INDEX IF EXISTS idx_scheduled_changes_retention;
