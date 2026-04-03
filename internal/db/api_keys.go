package db

import (
	"context"
	"fmt"
	"time"
)

type APIKey struct {
	ID        int       `json:"id"`
	KeyHash   string    `json:"key_hash"`
	Label     *string   `json:"label,omitempty"`
	RateLimit int       `json:"rate_limit"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) GetAPIKey(ctx context.Context, keyHash string) (APIKey, error) {
	var k APIKey
	err := s.pool.QueryRow(ctx, `
		SELECT id, key_hash, label, rate_limit, created_at
		FROM api_keys WHERE key_hash = $1
	`, keyHash).Scan(&k.ID, &k.KeyHash, &k.Label, &k.RateLimit, &k.CreatedAt)
	if err != nil {
		return APIKey{}, fmt.Errorf("getting API key: %w", err)
	}
	return k, nil
}
