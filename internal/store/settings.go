package store

import (
	"context"
	"time"

	"animarr/internal/store/db"
)

// Setting is one row in the settings table.
// Value is plain text for ordinary settings and ciphertext for secrets.
type Setting struct {
	Key       string
	Value     string
	Secret    bool
	UpdatedAt time.Time
}

func fromDBSetting(row db.Setting) Setting {
	return Setting{
		Key:       row.Key,
		Value:     row.Value,
		Secret:    bool(row.IsSecret),
		UpdatedAt: row.UpdatedAt.Time(),
	}
}

func toSettingParams(row Setting) db.UpsertSettingParams {
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = time.Now().UTC()
	}
	return db.UpsertSettingParams{
		Key:       row.Key,
		Value:     row.Value,
		IsSecret:  db.Bool(row.Secret),
		UpdatedAt: db.NewUnixTime(row.UpdatedAt),
	}
}

// ListSettings returns every saved setting.
func (s *Store) ListSettings(ctx context.Context) ([]Setting, error) {
	rows, err := s.q.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Setting, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromDBSetting(row))
	}
	return out, nil
}

// GetSetting loads one row.
func (s *Store) GetSetting(ctx context.Context, key string) (Setting, error) {
	row, err := s.q.GetSetting(ctx, key)
	if err != nil {
		return Setting{}, mapErr(err)
	}
	return fromDBSetting(row), nil
}

// UpsertSetting inserts or replaces one row.
func (s *Store) UpsertSetting(ctx context.Context, row Setting) error {
	return s.q.UpsertSetting(ctx, toSettingParams(row))
}

// DeleteSetting removes one row. A missing row is not an error.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	return s.q.DeleteSetting(ctx, key)
}

// RewriteSettings runs fn on the current rows and upserts what it returns, in one transaction.
// An error from fn rolls the transaction back and leaves every row as it was.
func (s *Store) RewriteSettings(ctx context.Context, fn func([]Setting) ([]Setting, error)) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	q := s.q.WithTx(tx)
	rows, err := q.ListSettings(ctx)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	cur := make([]Setting, 0, len(rows))
	for _, row := range rows {
		cur = append(cur, fromDBSetting(row))
	}
	next, err := fn(cur)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, row := range next {
		if err := q.UpsertSetting(ctx, toSettingParams(row)); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
