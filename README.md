# animarr

animarr tracks an AniList anime list, finds episode releases on Nyaa, sends them to qBittorrent, and hardlinks the video into a Jellyfin library. It is a small Go process meant to replace Sonarr and Prowlarr for an anime-only library. One AniList entry is one season. A franchise that spans several entries is one series folder. There is no TVDB season map.

Idle target is a single static binary and a SQLite file, on the order of a few tens of megabytes. The UI is server-rendered HTML with HTMX. There is no JavaScript build.

## Run

```bash
cd animarr
cp .env.example .env   # fill ANILIST_USERNAME and the API keys you use
go run ./cmd/animarr
```

The UI listens on `:8990`. `animarr health` checks `GET /healthz` on that address.

Docker, from this directory, with the vpn container already running:

```bash
docker compose up -d --build
```

The service uses `network_mode: container:vpn`, so it does not publish ports itself. See [Networking](#networking).

Commands:

| Command | What it does |
| --- | --- |
| `animarr` or `animarr serve` | Sync, poll Nyaa, import, and serve the UI |
| `animarr organize [--dry-run]` | Move standalone movies into their folders and backfill movie NFOs, season posters, and specials NFOs |
| `animarr rotate-key` | Re-encrypt saved secrets with a new key. See [Secrets](#secrets) |
| `animarr health` | Exit 0 when `/healthz` returns 200 |

## Config

A value saved on the Settings page overrides the environment variable of the same name. The environment overrides `CONFIG_FILE` (JSON). Built-in defaults are last. The page shows the effective value and its source (`saved`, `env`, `file`, or `default`). Reset deletes the saved row and the env, file, or default applies again. Clients and the scheduler read the effective config on the next sync, RSS poll, or download poll, so a save does not need a restart.

`AUTOMATION_ENABLED` defaults to false. While it is off, AniList sync and the pages still run, and the scheduler does not: no Nyaa RSS or backlog search, no automatic grab, and the download poll does not import, write NFO files, or refresh Jellyfin. Buttons on a show are not gated. Search, Re-download, Interactive search, and Grab run either way. Grab fails with a message on the page when qBittorrent is not configured (empty WebUI URL, or no client). A file grabbed while the switch is off sits in qBittorrent until you turn automation on; the next download poll then imports it, writes NFO, and refreshes Jellyfin. A banner on every page, and the Automation stat on the Dashboard, show the state and link to Settings. The scheduler reads the switch on every cycle. Turn it on in Settings, or set the variable to `true`, when you want scheduled grabs to start. `organize` is not gated by the switch.

`LISTEN_ADDR`, `DATA_PATH`, `LOG_LEVEL`, `CONFIG_FILE`, and `ANIMARR_SECRET_KEY` are read at startup and are not on the form. Everything else in the table is editable. The UI column says `yes` or `secret`. A secret field shows set or not set, never the value. Leaving it blank keeps the current secret. Check "Save empty" to store an empty secret, which overrides an env var. Empty `QB_USERNAME` and `QB_PASSWORD` together skip qBittorrent login (localhost bypass).

Save writes only fields that differ from the current effective value, so a click does not pin every env or default into the database. Test buttons on Nyaa, qBittorrent, and Jellyfin call those services with the values currently typed, before they are saved.

| Variable | Default | UI | Meaning |
| --- | --- | --- | --- |
| `AUTOMATION_ENABLED` | `false` | yes | Master switch for the scheduler. When false, AniList sync and the UI keep working, scheduled Nyaa searches and automatic grabs do not run, and the download poll does not import. Show-page Search, Re-download, Interactive search, and Grab still run. A saved value overrides this variable |
| `ANILIST_USERNAME` | empty | yes | Public AniList user. CURRENT and PLANNING are monitored when first added. A later sync does not turn monitoring back on. Empty skips the scheduled sync |
| `ANILIST_TOKEN` | empty | secret | Optional. Sent as `Authorization: Bearer` when set |
| `LISTEN_ADDR` | `:8990` | no | HTTP listen address. Restart to change it |
| `DATA_PATH` | `/config` | no | Directory for `animarr.db` and, when no env key is set, `secret.key` |
| `LIBRARY_DIR` | `/data/media/anime` | yes | Jellyfin library root inside this container |
| `DOWNLOADS_DIR` | `/data/downloads` | yes | qBittorrent download root. Grabs go in `animarr/` under this |
| `NYAA_URLS` | `https://nyaa.si` | yes | Comma-separated bases. The next one is tried when a request fails |
| `NYAA_CATEGORY` | `1_2` | yes | Nyaa category. `1_2` is English-translated anime |
| `NYAA_USER_AGENT` | `animarr/0.1 (+homelab; anime tracker)` | yes | Sent on Nyaa and AniList requests |
| `QB_URL` | `http://127.0.0.1:8080` | yes | qBittorrent WebUI. Same network namespace when using the vpn container |
| `QB_USERNAME` | empty | yes | WebUI user. Empty plus empty password skips login |
| `QB_PASSWORD` | empty | secret | WebUI password |
| `QB_CATEGORY` | `animarr` | yes | Category and tag applied to grabs |
| `JELLYFIN_URL` | `http://jellyfin:8096` | yes | Jellyfin base URL. See networking |
| `JELLYFIN_API_KEY` | empty | secret | Jellyfin API key. Refresh is skipped when this is empty |
| `SYNC_INTERVAL` | `30m` | yes | AniList sync period |
| `RSS_INTERVAL` | `15m` | yes | Nyaa recent-feed poll |
| `QB_POLL_INTERVAL` | `1m` | yes | qBittorrent progress poll |
| `RSS_WINDOW` | `6h` | yes | Episodes aired inside this window are left to the RSS poll. `0` searches them as backlog |
| `SEARCH_BACKOFF` | `6h` | yes | Minimum gap between backlog searches of one episode. `0` is allowed |
| `REQUEST_INTERVAL` | `2s` | yes | Minimum gap between Nyaa requests |
| `BACKLOG_PER_POLL` | `3` | yes | Older episodes searched directly each RSS poll |
| `SCORE_PREFERRED_RESOLUTION` | `1080p` | yes | Global preferred resolution (`480p`, `720p`, `1080p`, `2160p`). A show can override it on its page |
| `SCORE_HEVC_BONUS` | `10` | yes | Added for x265 / HEVC / H.265 |
| `SCORE_DUB_PENALTY` | `40` | yes | Subtracted for English-dub-only releases. Dual audio is not penalized |
| `SCORE_REPACK_BONUS` | `15` | yes | Added for repack, proper, or v2+ |
| `SCORE_MIN_SEEDERS` | `1` | yes | Releases below this are ignored |
| `SCORE_GROUP_ORDER` | `ToonsHub,Erai-raws,ASW,SubsPlease,Judas,DKB,EMBER` | yes | Earlier groups score higher |
| `SCORE_MATCH_THRESHOLD` | `0.82` | yes | Title similarity required to attach a release to a show |
| `UPGRADE_ENABLED` | `false` | yes | Replace a grabbed file when a much higher score appears |
| `UPGRADE_REPACK` | `true` | yes | Replace a file when a higher version (v2, repack, proper) appears |
| `UPGRADE_WINDOW` | `72h` | yes | How long after import an upgrade is still allowed |
| `UPGRADE_MIN_DELTA` | `15` | yes | Score gap required when `UPGRADE_ENABLED` is on |
| `NFO_ENABLED` | `true` | yes | Write `tvshow.nfo`, `season.nfo`, and episode `.nfo` files from AniList. `false` skips them |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn`, or `error`. Restart to change it |
| `CONFIG_FILE` | empty | no | JSON overlay, applied before env vars. Keys: `preferredResolution`, `hevcBonus`, `dubPenalty`, `repackBonus`, `minSeeders`, `groupOrder`, `matchThreshold`, `upgradeEnabled`, `upgradeRepack`, `upgradeWindow`, `upgradeMinDelta`, `nyaaUrls`, `nyaaCategory`, `nfoEnabled`, `automationEnabled` |
| `ANIMARR_SECRET_KEY` | empty | no | Key for secrets saved in the UI. See [Secrets](#secrets) |

Example file:

```json
{
  "preferredResolution": "1080p",
  "minSeeders": 2,
  "groupOrder": ["ToonsHub", "Erai-raws", "SubsPlease"]
}
```

There is no login. The process is meant to sit behind your VPN or tailnet and Caddy. Settings saves and connection tests require a same-site check: the form carries a token from an `animarr_csrf` cookie, and a request that sends `Origin` or `Referer` must name this host (or `X-Forwarded-Host`, which Caddy sets). Other pages do not check that token.

### Secrets

`ANILIST_TOKEN`, `QB_PASSWORD`, and `JELLYFIN_API_KEY` are secrets. A value that comes only from the environment stays in the environment and is not written to SQLite. A value saved in the UI is encrypted before it is stored.

Encryption is AES-256-GCM from the Go standard library. Each value gets a random 12-byte nonce. The stored text is `v1:` plus base64 of the nonce and ciphertext. The setting name is authenticated with the ciphertext, so a row cannot be moved onto another key. Secrets are not written to logs. If an error string contains one, the UI replaces it.

The key is 32 bytes. `ANIMARR_SECRET_KEY` wins when it is set. It may be those 32 bytes as standard, raw, or URL base64, or 64 hex characters. Anything else is a passphrase stretched with scrypt (`N=32768`, `r=8`, `p=1`, salt `animarr.settings.v1`). The salt is fixed, so the same passphrase always makes the same key. Prefer a random 32-byte key.

When that variable is unset, the first start writes a random key to `<DATA_PATH>/secret.key` with mode `0600`. A file this process just wrote that is group- or world-readable is refused. An existing file that is too open is still used, and the Settings page warns you to `chmod 600` it. A missing, unreadable, or wrong-length file is left in place. The process still starts, on a throwaway in-memory key, and saved secrets will not decrypt.

If decryption fails, the row is kept. The secret is treated as unset, including when an env var has a value, until you reset the row or type a new one. The Settings page and the log name the setting. They do not print the ciphertext or the plaintext.

`secret.key` sits next to `animarr.db`. A copy of the whole data directory (the compose `/config` mount) contains both the ciphertext and the key that opens it. Set `ANIMARR_SECRET_KEY` in the environment and do not rely on the file if that directory is backed up or shared. The env var is never written into the database.

`animarr rotate-key` re-encrypts every secret row in one transaction. `--old` or `ANIMARR_OLD_SECRET_KEY` is the current key. When both are empty, the command uses the key the process would load now. `--new` or `ANIMARR_NEW_SECRET_KEY` is required. A row that does not decrypt aborts the transaction and leaves the database unchanged. `--write-file auto` (the default) updates `secret.key` only when `ANIMARR_SECRET_KEY` is unset, because a set env var is what the next start will use and the file would be ignored. `true` always writes the file. `false` never does. After a rotation that does not write the file, set `ANIMARR_SECRET_KEY` to the new key before the next start. The command prints how many rows changed. It does not print the key. If the database commits and the file write then fails, the message says so and the new key is only the one you passed in.

## What the loop does

Automation is off until you enable it. The scheduler steps that search Nyaa, grab automatically, import, write NFO, or refresh Jellyfin do not run while it is off. AniList sync still does. Show-page buttons still search and grab. On startup, when the switch is off, the process logs that once.

1. Every 30 minutes, load the public AniList lists `CURRENT` and `PLANNING`. New rows of either status start monitored. Existing monitored flags are kept, including when you turn a show off. Shows that leave the list stay in the database.
2. Every 15 minutes, read the Nyaa RSS feed and match titles against monitored shows. Batch and season-pack titles (ranges like `01-12`, the words `Batch` and `Complete`) are ignored.
3. For wanted episodes older than the RSS window, search romaji and English titles, a few per poll, and dedupe by infohash.
4. Pick the highest score at or above the seeder floor and add a magnet in category `animarr`.
5. When qBittorrent finishes, hardlink the episode file and leave the download in place so it can seed. If the link fails (exFAT does not support hardlinks), the file is copied. The copy is written to a temporary name in the destination folder and renamed into place when it finishes, so a crash does not leave a partial file under the final name. The first copy in a process logs one warning, and the Settings page notes it while the latest import was a copy. That uses about twice the disk, because the download stays for seeding.
6. `POST /Library/Refresh` on Jellyfin when a key is set.

Library names:

- One franchise is one folder, named after the first TV entry's main title: `<LIBRARY_DIR>/<Root title>/Season 01/<Root title> - S01E07.mkv`
- Each later AniList TV entry in that chain is the next `Season NN`. Episode numbers restart at 1 for that entry. They are not continuous across the franchise.
- A single-entry show is still `<Title>/Season 01/`.
- Movies, OVAs, and specials that belong to a TV franchise share `Season 00`. Their episode numbers are blocks ordered by start year, then AniList id, so two one-episode OVAs do not both become `S00E01`.
- A movie with no TV franchise stays `<Title> (Year)/<Title> (Year).mkv`. A movie sequel is its own folder.
- A new sequel that shows up on the AniList list joins the existing folder as the next season on the next sync. The graph is only the shows already stored. A sequel whose prequel is not in the database yet keeps its own folder until that prequel is synced. After that, season numbers in the database can change. Files already on disk stay where they were imported. `animarr organize` moves standalone movies into their own folders and backfills NFOs and season posters. It does not renumber an existing season folder.

`SxxExx` in a new download uses the episode number on that AniList entry and the franchise season number. The season number inside a Nyaa release title is not used to pick a different entry. A standalone movie is one file, not `S01E01`.

When the chain is a single path of `SEQUEL` / `PREQUEL` links, that order is the season order. A branch or a cycle is ordered by start year, then AniList season (`WINTER`, `SPRING`, `SUMMER`, `FALL`), then id. That fallback only numbers folders.

Jellyfin metadata:

- `tvshow.nfo` sits in the series folder. `season.nfo` sits in each season folder. Each video animarr creates gets a sibling `.nfo`. Standalone movies get `movie.nfo` and a sibling `.nfo`.
- Titles, plot, genres, studio, year, and `<uniqueid type="anilist">` come from AniList. AniList does not provide episode titles, so the episode title is `Episode N` unless one was stored. The aired date comes from the airing schedule when AniList has it.
- `poster.jpg` and `folder.jpg` are the AniList cover. An existing non-empty `poster.jpg` is left alone.
- Writing the same NFO again replaces the XML. It is cheap. Set `NFO_ENABLED=false` to turn it off. New imports write NFO files when that switch is on. `animarr organize` backfills them for files already recorded.
- In the Jellyfin library, enable the NFO metadata reader. Disable the TVDB and TMDB metadata fetchers for this library, or leave them for artwork only. If those fetchers supply episode numbers, they will fight the local NFO numbering.

Search, ignore, unignore, and re-download are on the show page. Search can add a show that is not on the AniList user list. Those rows are monitored.

The Shows tab is one grid of cards. Filters (all, monitored, airing) and the sort (next airing, title, recently added) still apply. A franchise with more than one entry in the current filter is a single card: the root entry's title and cover, plus a badge like `4 seasons · 36/48 eps`. A single entry stays a normal show card. The franchise card opens `/franchises/{anilistId}`, which lists every stored entry of that franchise in air-date order. The date is the month and year of the first airing (`Apr 2019`). Movies, OVAs, and specials are labeled that way and sit by their date, not after the numbered seasons. Each row links to its show page. Add all seasons, on Search and on the franchise page, still adds the connected TV seasons.

Search on an episode still grabs the highest-scoring release. Interactive search, on each episode and as Search all releases on the show, lists every Nyaa hit for the show title: release name, size, seeders and leechers, published date, group, resolution, codec, and the score with a short reason list. Releases the automatic search would skip (wrong episode, batch, too few seeders, title mismatch) stay in the table and are marked Rejected. The table is sorted by score and the column headers re-sort it in the browser. Grab sends that release through the same qBittorrent, download-row, history, import, NFO, and Jellyfin path as an automatic grab. An episode Grab ties the torrent to that episode. A batch grabbed from Search all releases is tied to each episode in its range. These posts use the same-site token as Settings.

## Networking

animarr is intended to share the vpn container with Sonarr, Prowlarr, Whisparr, and qBittorrent.

- Nyaa traffic leaves through Mullvad.
- qBittorrent is `http://127.0.0.1:8080` in that namespace.
- Jellyfin is not in that namespace. It is the `jellyfin` container on the `media` network and on `homelab_proxy`. The vpn container is also on `homelab_proxy`, so `http://jellyfin:8096` resolves from animarr. Do not point this at `host.docker.internal`; the vpn service has no such host entry.

`DATA_DIR` on the animarr compose file must be the same host directory the sonarr stack mounts at `/data` (the one that contains `downloads/` and `media/`). The compose default `./data` is relative to `animarr/`, which is a different folder, and a hardlink across those mounts will fail and fall back to a copy. Jellyfin mounts `${DATA_DIR}/media` at `/media`, so a file animarr writes at `/data/media/anime/...` shows up in Jellyfin as `/media/anime/...`.

The image runs as uid 1000, matching the sonarr stack `PUID`, so hardlinked files stay readable.

Caddy proxies `animarr.$BASE_DOMAIN` to `animarr:8990`. Two edits are documented and not applied in `vpn/docker-compose.yml`:

- Add `animarr` to `networks.homelab_proxy.aliases` on the vpn service. Without it, Caddy cannot resolve the name.
- Optionally add `8990:8990` on the vpn service if you want the UI on the host without Caddy.

Recreating the vpn container orphans every container that shares its network. Recreate those stacks afterwards.

## Layout

`cmd/animarr` is the binary. `internal/` holds AniList, the indexer interface, Nyaa, the release parser, matcher, scorer, qBittorrent, Jellyfin, the importer, NFO writer, franchise layout, SQLite, settings, the secret box, the pipeline, the scheduler, and the UI. Nyaa is the indexer. Score weights live in `internal/tuning` so the config loader does not import the scorer.

SQLite uses `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`). Queries are generated with [sqlc](https://sqlc.dev) into `internal/store/db`. The schema is the numbered files in `sql/schema` (goose-style `-- +goose Up` sections). `sql/queries` holds the query files. `make generate` runs `sqlc generate`. The Docker image builds the committed Go and does not need sqlc. On startup the process applies any migration that is not yet in `schema_migrations`. `0001` matches the original tables, so a database created before sqlc opens and then gains the franchise columns from `0002`, the `settings` table from `0003`, and the start month and day from `0004`. The next AniList sync fills month and day on rows that were stored before `0004`.

```bash
make generate   # requires sqlc on PATH
make check      # CGO_ENABLED=0 go vet and go test
```

## Limitations

- Automation is off until `AUTOMATION_ENABLED` is turned on. A first start syncs AniList and does not schedule grabs or imports. Show-page buttons can still search and send a torrent; the file imports after the switch is on.
- Automatic search still skips season packs and batches. Interactive search lists them, marked Rejected, and Grab can send one. A pack that does not name its episode range has to be grabbed from an episode row.
- AniList airing times are the first page of the schedule (50 episodes) plus `nextAiringEpisode`.
- A Nyaa title like `S02E07` is episode 7 of the matched AniList entry. The library season folder comes from the franchise, not from that `S02`.
- Upgrades are off except repacks and v2+ inside the upgrade window.
- Per-show 2160p is a manual resolution override. It is not inferred.
- The UI has no authentication. Settings saves and connection tests check a same-site token. Keep the process behind the VPN or Caddy.
- Nyaa is the only indexer that searches. Mirrors are fallbacks for the same RSS API, not a different index.
- Live qBittorrent, Jellyfin, and Nyaa were not called from this repo's tests. Those clients are covered with `httptest`. The Settings page is covered the same way, not in a browser. The score step between groups is a code default and is not on the form.
- A settings row whose key is no longer known is ignored at startup. Old `SONARR_URL` and `SONARR_API_KEY` rows can stay in the database.
