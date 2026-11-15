-- +goose Up
-- One row per episode. A worker claims a pending row, or a running row whose
-- lease has expired, then reports done, skipped, or failed.
CREATE TABLE encodes (
    id INTEGER PRIMARY KEY,
    episode_id INTEGER NOT NULL UNIQUE REFERENCES episodes(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    state TEXT NOT NULL,
    codec TEXT NOT NULL DEFAULT '',
    encoder TEXT NOT NULL DEFAULT '',
    src_size INTEGER NOT NULL DEFAULT 0,
    dst_size INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    progress REAL NOT NULL DEFAULT 0,
    fps REAL NOT NULL DEFAULT 0,
    speed REAL NOT NULL DEFAULT 0,
    lease_until INTEGER,
    started_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_encodes_state ON encodes(state, id);

-- +goose Down
-- DROP TABLE encodes;
-- sqlc reads this file as schema, so the drop stays commented out.
