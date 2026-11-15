-- +goose Up
-- AniList startDate month and day. Year already lives in start_year.
-- 0 means AniList did not send that part. The next sync fills existing rows.
ALTER TABLE shows ADD COLUMN start_month INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shows ADD COLUMN start_day INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- SQLite cannot drop these columns in place. Restoring the old schema means a new database.
