package mysql

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/openchat/openchat/server/store/types"
)

// CreateAuthService inserts or updates an internal service credential.
func (a *Adapter) CreateAuthService(service *types.AuthService) (int64, error) {
	if service == nil {
		return 0, fmt.Errorf("auth service is nil")
	}
	scopes, err := json.Marshal(service.Scopes)
	if err != nil {
		return 0, fmt.Errorf("marshal auth service scopes: %w", err)
	}

	res, err := a.db.Exec(
		`INSERT INTO auth_services (slug, name, token_prefix, token_hash, scopes, state)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   name = VALUES(name),
		   token_prefix = VALUES(token_prefix),
		   token_hash = VALUES(token_hash),
		   scopes = VALUES(scopes),
		   state = VALUES(state),
		   last_used_at = NULL`,
		service.Slug,
		service.Name,
		service.TokenPrefix,
		service.TokenHash,
		scopes,
		service.State,
	)
	if err != nil {
		return 0, fmt.Errorf("create auth service: %w", err)
	}
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		return id, nil
	}
	var id int64
	if err := a.db.QueryRow(`SELECT id FROM auth_services WHERE slug = ?`, service.Slug).Scan(&id); err != nil {
		return 0, fmt.Errorf("lookup auth service id: %w", err)
	}
	return id, nil
}

// ListAuthServices returns all registered account-center services.
func (a *Adapter) ListAuthServices() ([]*types.AuthService, error) {
	rows, err := a.db.Query(
		`SELECT id, slug, name, token_prefix, scopes, state, last_used_at, created_at, updated_at
		 FROM auth_services
		 ORDER BY id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list auth services: %w", err)
	}
	defer rows.Close()

	var services []*types.AuthService
	for rows.Next() {
		service, err := scanAuthService(rows)
		if err != nil {
			return nil, err
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

// GetAuthServiceByTokenHash retrieves an active service by token hash.
func (a *Adapter) GetAuthServiceByTokenHash(tokenHash string) (*types.AuthService, error) {
	row := a.db.QueryRow(
		`SELECT id, slug, name, token_prefix, scopes, state, last_used_at, created_at, updated_at
		 FROM auth_services
		 WHERE token_hash = ? AND state = 0`,
		tokenHash,
	)
	service, err := scanAuthService(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get auth service by token hash: %w", err)
	}
	return service, nil
}

// RevokeAuthService disables a service credential.
func (a *Adapter) RevokeAuthService(id int64) error {
	_, err := a.db.Exec(`UPDATE auth_services SET state = 1 WHERE id = ?`, id)
	return err
}

// TouchAuthServiceLastUsed updates a service's last-used timestamp.
func (a *Adapter) TouchAuthServiceLastUsed(id int64) error {
	_, err := a.db.Exec(`UPDATE auth_services SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

type authServiceScanner interface {
	Scan(dest ...interface{}) error
}

func scanAuthService(scanner authServiceScanner) (*types.AuthService, error) {
	var service types.AuthService
	var scopesRaw []byte
	var lastUsed sql.NullTime
	if err := scanner.Scan(
		&service.ID,
		&service.Slug,
		&service.Name,
		&service.TokenPrefix,
		&scopesRaw,
		&service.State,
		&lastUsed,
		&service.CreatedAt,
		&service.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(scopesRaw) > 0 {
		_ = json.Unmarshal(scopesRaw, &service.Scopes)
	}
	if service.Scopes == nil {
		service.Scopes = []string{}
	}
	if lastUsed.Valid {
		service.LastUsedAt = &lastUsed.Time
	}
	return &service, nil
}
