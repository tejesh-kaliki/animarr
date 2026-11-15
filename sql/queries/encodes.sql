-- name: GetEncode :one
SELECT * FROM encodes WHERE id = ?;

-- name: GetEncodeByEpisode :one
SELECT * FROM encodes WHERE episode_id = ?;

-- name: InsertEncode :one
INSERT INTO encodes (
    episode_id, file_path, state, created_at, updated_at
) VALUES (?, ?, 'pending', ?, ?)
RETURNING *;

-- name: ResetEncodePending :execrows
UPDATE encodes
SET file_path = ?,
    state = 'pending',
    codec = '',
    encoder = '',
    src_size = 0,
    dst_size = 0,
    error = '',
    progress = 0,
    fps = 0,
    speed = 0,
    lease_until = NULL,
    started_at = NULL,
    updated_at = ?
WHERE episode_id = ?
  AND state != 'running'
  AND NOT (state IN ('done', 'pending', 'skipped') AND file_path = ?);

-- name: SelectClaimableEncodeID :one
SELECT id FROM encodes
WHERE state = 'pending'
   OR (state = 'running' AND (lease_until IS NULL OR lease_until <= ?))
ORDER BY id
LIMIT 1;

-- name: MarkEncodeRunning :execrows
UPDATE encodes
SET state = 'running',
    lease_until = ?,
    started_at = ?,
    updated_at = ?,
    progress = 0,
    fps = 0,
    speed = 0,
    error = ''
WHERE id = ?
  AND (
    state = 'pending'
    OR (state = 'running' AND (lease_until IS NULL OR lease_until <= ?))
  );

-- name: HeartbeatEncode :execrows
UPDATE encodes
SET lease_until = ?,
    updated_at = ?,
    encoder = ?,
    progress = ?,
    fps = ?,
    speed = ?
WHERE id = ? AND state = 'running';

-- name: FinishEncode :execrows
UPDATE encodes
SET state = ?,
    file_path = ?,
    codec = ?,
    encoder = ?,
    src_size = ?,
    dst_size = ?,
    error = ?,
    progress = ?,
    speed = ?,
    fps = 0,
    lease_until = NULL,
    updated_at = ?
WHERE id = ? AND state = 'running';

-- name: ReleaseEncode :execrows
UPDATE encodes
SET state = 'pending',
    lease_until = NULL,
    progress = 0,
    fps = 0,
    speed = 0,
    error = '',
    started_at = NULL,
    updated_at = ?
WHERE id = ? AND state = 'running';

-- name: RequeueFailedEncodes :execrows
UPDATE encodes
SET state = 'pending',
    error = '',
    progress = 0,
    fps = 0,
    speed = 0,
    lease_until = NULL,
    started_at = NULL,
    updated_at = ?
WHERE state = 'failed';

-- name: ListEncodes :many
SELECT
    e.id, e.episode_id, e.file_path, e.state, e.codec, e.encoder,
    e.src_size, e.dst_size, e.error, e.progress, e.fps, e.speed,
    e.lease_until, e.started_at, e.created_at, e.updated_at,
    ep.show_id, ep.number AS episode_number,
    s.title_english, s.title_romaji, s.title_native
FROM encodes e
JOIN episodes ep ON ep.id = e.episode_id
JOIN shows s ON s.id = ep.show_id
ORDER BY
    CASE e.state WHEN 'running' THEN 0 WHEN 'pending' THEN 1 WHEN 'failed' THEN 2 ELSE 3 END,
    e.id
LIMIT ?;

-- name: CountEncodesByState :many
SELECT state, COUNT(*) AS n FROM encodes GROUP BY state;

-- name: SetEpisodeFilePath :exec
UPDATE episodes SET file_path = ? WHERE id = ?;
