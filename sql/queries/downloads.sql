-- name: InsertDownload :one
INSERT INTO downloads (
    show_id, episode, info_hash, title, save_path, state, progress, score, added_at, error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListDownloadsByState :many
SELECT
    d.id, d.show_id, d.episode, d.info_hash, d.title, d.save_path, d.state,
    d.progress, d.score, d.added_at, d.error,
    COALESCE(NULLIF(s.title_english, ''), NULLIF(s.title_romaji, ''), s.title_native, '') AS show_title
FROM downloads d
LEFT JOIN shows s ON s.id = d.show_id
WHERE d.state IN (sqlc.slice('states'))
ORDER BY d.id DESC;

-- name: UpdateDownload :exec
UPDATE downloads SET state = ?, progress = ?, error = ? WHERE id = ?;
