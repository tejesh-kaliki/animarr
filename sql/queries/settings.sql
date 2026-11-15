-- name: ListSettings :many
SELECT key, value, is_secret, updated_at
FROM settings
ORDER BY key;

-- name: GetSetting :one
SELECT key, value, is_secret, updated_at
FROM settings
WHERE key = ?;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value, is_secret, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
    value = excluded.value,
    is_secret = excluded.is_secret,
    updated_at = excluded.updated_at;

-- name: DeleteSetting :exec
DELETE FROM settings WHERE key = ?;
