package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"animarr/sql/schema"
)

// migrate applies numbered SQL files in sql/schema and records them in schema_migrations.
// 0001 matches the original hand-written schema (CREATE TABLE IF NOT EXISTS), so an
// existing database upgrades without rewriting rows.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at INTEGER NOT NULL
)`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	entries, err := schema.Files.ReadDir(".")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := schema.Files.ReadFile(name)
		if err != nil {
			return err
		}
		if err := s.applyMigration(name, upSection(string(body))); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(name, up string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range splitSQL(up) {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, name, time.Now().UTC().Unix()); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return tx.Commit()
}

// upSection returns the goose Up section. Files with no markers are all-up.
func upSection(content string) string {
	if !strings.Contains(strings.ToLower(content), "+goose") {
		return content
	}
	var b strings.Builder
	down := false
	started := false
	for _, line := range strings.Split(content, "\n") {
		t := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(t, "-- +goose") {
			rest := strings.TrimSpace(strings.TrimPrefix(t, "-- +goose"))
			switch {
			case strings.HasPrefix(rest, "up"):
				down = false
				started = true
			case strings.HasPrefix(rest, "down"):
				down = true
			}
			continue
		}
		if started && !down {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func splitSQL(sqlText string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	runes := []rune(sqlText)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '-' && i+1 < len(runes) && runes[i+1] == '-' {
			for ; i < len(runes) && runes[i] != '\n'; i++ {
				b.WriteRune(runes[i])
			}
			if i < len(runes) {
				b.WriteRune('\n')
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			b.WriteRune(r)
			continue
		}
		if r == ';' {
			if stmt := strings.TrimSpace(b.String()); stmt != "" && !onlyComments(stmt) {
				out = append(out, stmt)
			}
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	if stmt := strings.TrimSpace(b.String()); stmt != "" && !onlyComments(stmt) {
		out = append(out, stmt)
	}
	return out
}

func onlyComments(stmt string) bool {
	for _, line := range strings.Split(stmt, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		return false
	}
	return true
}
