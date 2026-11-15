-- name: InsertEpisode :exec
INSERT INTO episodes (show_id, number, air_date, state, version)
VALUES (?, ?, ?, ?, 1);

-- name: UpdateEpisodeStateAndAir :exec
UPDATE episodes SET state = ?, air_date = ? WHERE show_id = ? AND number = ?;

-- name: GetEpisode :one
SELECT * FROM episodes WHERE show_id = ? AND number = ?;

-- name: ListEpisodesByShow :many
SELECT * FROM episodes WHERE show_id = ? ORDER BY number;

-- name: UpdateEpisode :exec
UPDATE episodes SET
    state = ?,
    release_title = ?,
    info_hash = ?,
    file_path = ?,
    score = ?,
    version = ?,
    downloaded_at = ?,
    last_search_at = ?
WHERE show_id = ? AND number = ?;

-- name: SetEpisodeState :execrows
UPDATE episodes SET state = ? WHERE show_id = ? AND number = ?;

-- name: TouchEpisodeSearch :exec
UPDATE episodes SET last_search_at = ? WHERE show_id = ? AND number = ?;

-- name: WantedBacklog :many
SELECT
    e.id, e.show_id, e.number, e.air_date, e.state, e.release_title, e.info_hash,
    e.file_path, e.score, e.version, e.downloaded_at, e.last_search_at,
    s.id AS show_pk, s.anilist_id, s.title_romaji, s.title_english, s.title_native,
    s.synonyms, s.format, s.status, s.list_status, s.episodes, s.next_airing_episode,
    s.next_airing_at, s.cover_image, s.season, s.season_year, s.start_year, s.start_month, s.start_day,
    s.monitored, s.preferred_resolution, s.relations_json, s.created_at, s.updated_at,
    s.description, s.genres, s.studio, s.franchise_id, s.season_number
FROM episodes e
JOIN shows s ON s.id = e.show_id
WHERE s.monitored = 1
  AND e.state = ?
  AND (e.air_date IS NULL OR e.air_date <= ?)
  AND (e.last_search_at IS NULL OR e.last_search_at <= ?)
ORDER BY e.air_date DESC, e.number DESC
LIMIT ?;

-- name: PromoteAired :execrows
UPDATE episodes
SET state = ?
WHERE state = ? AND air_date IS NOT NULL AND air_date <= ?;

-- name: CountWantedMonitored :one
SELECT COUNT(*)
FROM episodes e
JOIN shows s ON s.id = e.show_id
WHERE s.monitored = 1 AND e.state = ?;
