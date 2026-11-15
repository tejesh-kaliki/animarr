package settings

import (
	"context"
	"fmt"
	"time"

	"animarr/internal/secret"
	"animarr/internal/store"
)

// Rotate re-encrypts every secret row with newBox.
// A row that oldBox cannot open aborts the transaction and leaves the table unchanged.
func Rotate(ctx context.Context, st *store.Store, oldBox, newBox *secret.Box) (int, error) {
	if st == nil || oldBox == nil || newBox == nil {
		return 0, fmt.Errorf("rotate-key needs a database, an old key, and a new key")
	}
	var n int
	err := st.RewriteSettings(ctx, func(rows []store.Setting) ([]store.Setting, error) {
		out := make([]store.Setting, 0)
		now := time.Now().UTC()
		for _, row := range rows {
			if !row.Secret {
				continue
			}
			plain, err := oldBox.Decrypt(row.Key, row.Value)
			if err != nil {
				return nil, fmt.Errorf("cannot decrypt %s", row.Key)
			}
			enc, err := newBox.Encrypt(row.Key, plain)
			if err != nil {
				return nil, fmt.Errorf("cannot encrypt %s", row.Key)
			}
			row.Value = enc
			row.UpdatedAt = now
			out = append(out, row)
		}
		n = len(out)
		return out, nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}
