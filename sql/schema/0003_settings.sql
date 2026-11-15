-- +goose Up
-- UI settings. Non-secrets are plain text. Secrets are v1 AES-GCM ciphertext.
-- The application decides which keys are secret. The column records that choice.
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    is_secret INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL
);

-- +goose Down
-- DROP TABLE settings;
-- sqlc reads this file as schema, so the drop stays commented out.
