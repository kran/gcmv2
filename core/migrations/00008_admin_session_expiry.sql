-- +goose Up
ALTER TABLE accounts ADD COLUMN session_expires_at TIMESTAMP;

-- +goose Down
ALTER TABLE accounts DROP COLUMN session_expires_at;
