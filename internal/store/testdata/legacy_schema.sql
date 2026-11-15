CREATE TABLE IF NOT EXISTS shows (
    id INTEGER PRIMARY KEY,
    anilist_id INTEGER NOT NULL UNIQUE,
    title_romaji TEXT NOT NULL DEFAULT '',
    title_english TEXT NOT NULL DEFAULT '',
    title_native TEXT NOT NULL DEFAULT '',
    synonyms TEXT NOT NULL DEFAULT '[]',
    format TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    list_status TEXT NOT NULL DEFAULT '',
    episodes INTEGER NOT NULL DEFAULT 0,
    next_airing_episode INTEGER NOT NULL DEFAULT 0,
    next_airing_at INTEGER,
    cover_image TEXT NOT NULL DEFAULT '',
    season TEXT NOT NULL DEFAULT '',
    season_year INTEGER NOT NULL DEFAULT 0,
    start_year INTEGER NOT NULL DEFAULT 0,
    monitored INTEGER NOT NULL DEFAULT 0,
    preferred_resolution TEXT NOT NULL DEFAULT '',
    relations_json TEXT NOT NULL DEFAULT '[]',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS episodes (
    id INTEGER PRIMARY KEY,
    show_id INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
    number INTEGER NOT NULL,
    air_date INTEGER,
    state TEXT NOT NULL,
    release_title TEXT NOT NULL DEFAULT '',
    info_hash TEXT NOT NULL DEFAULT '',
    file_path TEXT NOT NULL DEFAULT '',
    score INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    downloaded_at INTEGER,
    last_search_at INTEGER,
    UNIQUE(show_id, number)
);
CREATE INDEX IF NOT EXISTS idx_episodes_state ON episodes(state);
CREATE TABLE IF NOT EXISTS downloads (
    id INTEGER PRIMARY KEY,
    show_id INTEGER NOT NULL,
    episode INTEGER NOT NULL,
    info_hash TEXT NOT NULL,
    title TEXT NOT NULL,
    save_path TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL,
    progress INTEGER NOT NULL DEFAULT 0,
    score INTEGER NOT NULL DEFAULT 0,
    added_at INTEGER NOT NULL,
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_downloads_state ON downloads(state);
CREATE INDEX IF NOT EXISTS idx_downloads_hash ON downloads(info_hash);
CREATE TABLE IF NOT EXISTS history (
    id INTEGER PRIMARY KEY,
    at INTEGER NOT NULL,
    level TEXT NOT NULL,
    message TEXT NOT NULL,
    show_id INTEGER,
    episode INTEGER
);
CREATE INDEX IF NOT EXISTS idx_history_at ON history(at DESC);
