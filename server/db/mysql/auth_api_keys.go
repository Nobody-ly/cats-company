package mysql

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/openchat/openchat/server/store/types"
)

// CreateAuthAPIKey inserts a user-owned service API key.
func (a *Adapter) CreateAuthAPIKey(key *types.AuthAPIKey) (int64, error) {
	if key == nil {
		return 0, fmt.Errorf("auth api key is nil")
	}
	scopes, err := json.Marshal(key.Scopes)
	if err != nil {
		return 0, fmt.Errorf("marshal auth api key scopes: %w", err)
	}

	res, err := a.db.Exec(
		`INSERT INTO auth_api_keys
		 (owner_user_id, service_slug, name, key_prefix, key_hash, scopes, state, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		key.OwnerUserID,
		key.ServiceSlug,
		key.Name,
		key.KeyPrefix,
		key.KeyHash,
		scopes,
		key.State,
		key.ExpiresAt,
	)
	if err != nil {
		return 0, fmt.Errorf("create auth api key: %w", err)
	}
	return res.LastInsertId()
}

// ListAuthAPIKeysByOwner returns user-owned API keys without secret material.
func (a *Adapter) ListAuthAPIKeysByOwner(ownerUserID int64) ([]*types.AuthAPIKey, error) {
	rows, err := a.db.Query(
		`SELECT id, owner_user_id, service_slug, name, key_prefix, scopes, state, last_used_at, expires_at, created_at, updated_at
		 FROM auth_api_keys
		 WHERE owner_user_id = ?
		 ORDER BY id DESC`,
		ownerUserID,
	)
	if err != nil {
		return nil, fmt.Errorf("list auth api keys: %w", err)
	}
	defer rows.Close()

	var keys []*types.AuthAPIKey
	for rows.Next() {
		key, err := scanAuthAPIKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// GetAuthAPIKeyByHash retrieves an active API key by hash.
func (a *Adapter) GetAuthAPIKeyByHash(keyHash string) (*types.AuthAPIKey, error) {
	row := a.db.QueryRow(
		`SELECT id, owner_user_id, service_slug, name, key_prefix, scopes, state, last_used_at, expires_at, created_at, updated_at
		 FROM auth_api_keys
		 WHERE key_hash = ? AND state = 0`,
		keyHash,
	)
	key, err := scanAuthAPIKey(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get auth api key by hash: %w", err)
	}
	return key, nil
}

// RevokeAuthAPIKey disables a user's API key.
func (a *Adapter) RevokeAuthAPIKey(ownerUserID, id int64) error {
	_, err := a.db.Exec(`UPDATE auth_api_keys SET state = 1 WHERE owner_user_id = ? AND id = ?`, ownerUserID, id)
	return err
}

// TouchAuthAPIKeyLastUsed updates a key's last-used timestamp.
func (a *Adapter) TouchAuthAPIKeyLastUsed(id int64) error {
	_, err := a.db.Exec(`UPDATE auth_api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

type authAPIKeyScanner interface {
	Scan(dest ...interface{}) error
}

func scanAuthAPIKey(scanner authAPIKeyScanner) (*types.AuthAPIKey, error) {
	var key types.AuthAPIKey
	var scopesRaw []byte
	var lastUsed sql.NullTime
	var expiresAt sql.NullTime
	if err := scanner.Scan(
		&key.ID,
		&key.OwnerUserID,
		&key.ServiceSlug,
		&key.Name,
		&key.KeyPrefix,
		&scopesRaw,
		&key.State,
		&lastUsed,
		&expiresAt,
		&key.CreatedAt,
		&key.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(scopesRaw) > 0 {
		_ = json.Unmarshal(scopesRaw, &key.Scopes)
	}
	if key.Scopes == nil {
		key.Scopes = []string{}
	}
	if lastUsed.Valid {
		key.LastUsedAt = &lastUsed.Time
	}
	if expiresAt.Valid {
		key.ExpiresAt = &expiresAt.Time
	}
	return &key, nil
}
