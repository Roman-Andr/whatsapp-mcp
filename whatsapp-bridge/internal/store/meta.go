package store

import (
	"context"
)

// SetMeta stores a bridge_meta value; an empty value removes the key.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	if value == "" {
		_, err := s.db.ExecContext(ctx, "DELETE FROM bridge_meta WHERE key = ?", key)
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO bridge_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
