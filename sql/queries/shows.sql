-- name: InsertShow :one
INSERT INTO shows (
    anilist_id, title_romaji, title_english, title_native, synonyms,
    format, status, list_status, episodes, next_airing_episode, next_airing_at,
    cover_image, season, season_year, start_year, monitored, preferred_resolution,
    relations_json, created_at, updated_at, description, genres, studio
) VALUES (
    sqlc.arg(anilist_id), sqlc.arg(title_romaji), sqlc.arg(title_english), sqlc.arg(title_native), sqlc.arg(synonyms),
    sqlc.arg(format), sqlc.arg(status), sqlc.arg(list_status), sqlc.arg(episodes), sqlc.arg(next_airing_episode), sqlc.arg(next_airing_at),
    sqlc.arg(cover_image), sqlc.arg(season), sqlc.arg(season_year), sqlc.arg(start_year), sqlc.arg(monitored), sqlc.arg(preferred_resolution),
    sqlc.arg(relations_json), sqlc.arg(created_at), sqlc.arg(updated_at), sqlc.arg(description), sqlc.arg(genres), sqlc.arg(studio)
)
RETURNING *;

-- name: UpdateShow :exec
UPDATE shows SET
    title_romaji = sqlc.arg(title_romaji),
    title_english = sqlc.arg(title_english),
    title_native = sqlc.arg(title_native),
    synonyms = sqlc.arg(synonyms),
    format = sqlc.arg(format),
    status = sqlc.arg(status),
    list_status = sqlc.arg(list_status),
    episodes = sqlc.arg(episodes),
    next_airing_episode = sqlc.arg(next_airing_episode),
    next_airing_at = sqlc.arg(next_airing_at),
    cover_image = sqlc.arg(cover_image),
    season = sqlc.arg(season),
    season_year = sqlc.arg(season_year),
    start_year = sqlc.arg(start_year),
    monitored = sqlc.arg(monitored),
    preferred_resolution = sqlc.arg(preferred_resolution),
    relations_json = sqlc.arg(relations_json),
    description = sqlc.arg(description),
    genres = sqlc.arg(genres),
    studio = sqlc.arg(studio),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: SetFranchise :execrows
UPDATE shows
SET franchise_id = ?, season_number = ?, updated_at = ?
WHERE id = ?;

-- name: GetShow :one
SELECT * FROM shows WHERE id = ?;

-- name: GetShowByAniList :one
SELECT * FROM shows WHERE anilist_id = ?;

-- name: ListShows :many
SELECT shows.*,
    (SELECT COUNT(*) FROM episodes e WHERE e.show_id = shows.id) AS total_eps,
    (SELECT COUNT(*) FROM episodes e WHERE e.show_id = shows.id AND e.state = 'downloaded') AS downloaded
FROM shows
ORDER BY CASE WHEN shows.status = 'RELEASING' THEN 0 ELSE 1 END, shows.title_romaji COLLATE NOCASE;

-- name: ListShowsMonitored :many
SELECT shows.*,
    (SELECT COUNT(*) FROM episodes e WHERE e.show_id = shows.id) AS total_eps,
    (SELECT COUNT(*) FROM episodes e WHERE e.show_id = shows.id AND e.state = 'downloaded') AS downloaded
FROM shows
WHERE monitored = 1
ORDER BY CASE WHEN shows.status = 'RELEASING' THEN 0 ELSE 1 END, shows.title_romaji COLLATE NOCASE;

-- name: ListShowsAiring :many
SELECT shows.*,
    (SELECT COUNT(*) FROM episodes e WHERE e.show_id = shows.id) AS total_eps,
    (SELECT COUNT(*) FROM episodes e WHERE e.show_id = shows.id AND e.state = 'downloaded') AS downloaded
FROM shows
WHERE status = 'RELEASING' OR (next_airing_at IS NOT NULL AND next_airing_at >= ?)
ORDER BY CASE WHEN shows.status = 'RELEASING' THEN 0 ELSE 1 END, shows.title_romaji COLLATE NOCASE;

-- name: SetMonitored :execrows
UPDATE shows SET monitored = ?, updated_at = ? WHERE id = ?;

-- name: SetResolution :execrows
UPDATE shows SET preferred_resolution = ?, updated_at = ? WHERE id = ?;

-- name: CountShows :one
SELECT COUNT(*) FROM shows;
