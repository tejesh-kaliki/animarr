package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestLegacySchemaUpgrades(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "animarr.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("testdata/legacy_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(string(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO shows (
		anilist_id, title_romaji, title_english, synonyms, format, status, list_status,
		episodes, monitored, relations_json, created_at, updated_at
	) VALUES (7, 'Legacy', 'Legacy English', '["Alt"]', 'TV', 'FINISHED', 'CURRENT', 3, 1, '[]', 100, 100)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO episodes (show_id, number, state) VALUES (1, 1, 'downloaded')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	sh, err := s.GetShowByAniList(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if sh.TitleEnglish != "Legacy English" || !sh.Monitored || len(sh.Synonyms) != 1 || sh.Synonyms[0] != "Alt" {
		t.Fatalf("%+v", sh)
	}
	if sh.SeasonNumber != 1 || sh.FranchiseID != 0 {
		t.Fatalf("legacy defaults franchise %d season %d", sh.FranchiseID, sh.SeasonNumber)
	}
	var gotDesc string
	if err := s.db.QueryRow(`SELECT description FROM shows WHERE anilist_id = 7`).Scan(&gotDesc); err != nil {
		t.Fatal(err)
	}
	if gotDesc != "" {
		t.Fatalf("description %q", gotDesc)
	}
	ep, err := s.GetEpisode(context.Background(), sh.ID, 1)
	if err != nil || ep.State != StateDownloaded {
		t.Fatalf("%+v %v", ep, err)
	}
	var versions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions < 1 {
		t.Fatalf("versions %d", versions)
	}
	var one int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_init.sql'`).Scan(&one); err != nil {
		t.Fatal(err)
	}
	if one != 1 {
		t.Fatalf("0001 applied %d times", one)
	}

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	var versionsAgain int
	if err := again.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_init.sql'`).Scan(&versionsAgain); err != nil {
		t.Fatal(err)
	}
	if versionsAgain != 1 {
		t.Fatalf("reopen applied 0001 %d times", versionsAgain)
	}
}

func TestFreshDatabaseRecordsMigrations(t *testing.T) {
	s := openTest(t)
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = '0001_init.sql'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("fresh migrations %d", n)
	}
	if _, err := s.db.Exec(`SELECT id FROM shows LIMIT 1`); err != nil {
		t.Fatal(err)
	}
}

func TestUpSectionSkipsDown(t *testing.T) {
	got := upSection("-- +goose Up\nCREATE TABLE a (id INT);\n-- +goose Down\nDROP TABLE a;\n")
	stmts := splitSQL(got)
	if len(stmts) != 1 || stmts[0] != "CREATE TABLE a (id INT)" {
		t.Fatalf("%q", stmts)
	}
}

func TestSplitSQLIgnoresSemicolonInComment(t *testing.T) {
	stmts := splitSQL("-- secret; kept\nCREATE TABLE settings (key TEXT);\n")
	if len(stmts) != 1 || !strings.Contains(stmts[0], "CREATE TABLE settings") {
		t.Fatalf("%q", stmts)
	}
}
