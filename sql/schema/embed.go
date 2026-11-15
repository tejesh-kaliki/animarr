// Package schema is the goose-style SQLite migration set, embedded at build time.
// sqlc reads the same .sql files; the binary does not need sqlc installed.
package schema

import "embed"

// Files contains the numbered migrations in this directory.
//
//go:embed *.sql
var Files embed.FS
