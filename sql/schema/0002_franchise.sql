-- +goose Up
-- Franchise layout and the AniList fields Jellyfin NFO files need.
-- Defaults keep rows written by 0001 valid.
ALTER TABLE shows ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE shows ADD COLUMN genres TEXT NOT NULL DEFAULT '[]';
ALTER TABLE shows ADD COLUMN studio TEXT NOT NULL DEFAULT '';
ALTER TABLE shows ADD COLUMN franchise_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shows ADD COLUMN season_number INTEGER NOT NULL DEFAULT 1;

-- +goose Down
-- SQLite cannot drop these columns in place. Restoring the old schema means a new database.
