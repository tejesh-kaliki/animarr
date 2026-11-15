-- name: InsertHistory :exec
INSERT INTO history (at, level, message, show_id, episode)
VALUES (?, ?, ?, ?, ?);

-- name: ListHistory :many
SELECT id, at, level, message, show_id, episode
FROM history
ORDER BY id DESC
LIMIT ?;

-- name: ListAiring :many
SELECT
    s.id AS show_id,
    COALESCE(NULLIF(s.title_english, ''), NULLIF(s.title_romaji, ''), s.title_native, '') AS title,
    s.cover_image AS cover,
    e.number AS episode,
    e.air_date AS at
FROM episodes e
JOIN shows s ON s.id = e.show_id
WHERE e.state = ? AND e.air_date IS NOT NULL AND e.air_date <= ?
ORDER BY e.air_date ASC
LIMIT 12;
