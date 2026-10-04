-- +goose Up
ALTER TABLE scheduled_changes ADD COLUMN kind TEXT NOT NULL DEFAULT 'records';
ALTER TABLE scheduled_changes ADD COLUMN payload TEXT;

-- +goose Down
ALTER TABLE scheduled_changes DROP COLUMN payload;
ALTER TABLE scheduled_changes DROP COLUMN kind;
