package mysql

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openchat/openchat/server/store/types"
)

const weixinClawBotTokenColumns = `
	id, token_hash, bot_token, token_last4, status, owner_uid, ilink_bot_id,
	ilink_user_id, base_url, source_scene_key, get_updates_buf, context_tokens, last_poll_at, last_used_at,
	last_error_at, last_error, created_at, updated_at`

func (a *Adapter) UpsertWeixinClawBotToken(token *types.WeixinClawBotToken) (*types.WeixinClawBotToken, error) {
	if token == nil || strings.TrimSpace(token.TokenHash) == "" || strings.TrimSpace(token.BotToken) == "" || token.OwnerUID <= 0 {
		return nil, fmt.Errorf("invalid weixin clawbot token")
	}
	status := strings.TrimSpace(token.Status)
	if status == "" {
		status = types.WeixinClawBotTokenActive
	}
	contextJSON, err := json.Marshal(normalizeWeixinClawBotContexts(token.ContextTokens))
	if err != nil {
		return nil, err
	}
	res, err := a.db.Exec(
		`INSERT INTO weixin_clawbot_tokens (
		     token_hash, bot_token, token_last4, status, owner_uid, ilink_bot_id,
		     ilink_user_id, base_url, source_scene_key, get_updates_buf, context_tokens, last_error
		 )
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')
		 ON DUPLICATE KEY UPDATE
		     bot_token = VALUES(bot_token),
		     token_last4 = VALUES(token_last4),
		     status = VALUES(status),
		     owner_uid = VALUES(owner_uid),
		     ilink_bot_id = VALUES(ilink_bot_id),
		     ilink_user_id = VALUES(ilink_user_id),
		     base_url = VALUES(base_url),
		     source_scene_key = VALUES(source_scene_key),
		     context_tokens = IF(JSON_LENGTH(VALUES(context_tokens)) = 0, context_tokens, VALUES(context_tokens)),
		     last_error = '',
		     last_error_at = NULL,
		     updated_at = CURRENT_TIMESTAMP`,
		strings.TrimSpace(token.TokenHash),
		strings.TrimSpace(token.BotToken),
		strings.TrimSpace(token.TokenLast4),
		status,
		token.OwnerUID,
		strings.TrimSpace(token.ILinkBotID),
		strings.TrimSpace(token.ILinkUserID),
		strings.TrimSpace(token.BaseURL),
		strings.TrimSpace(token.SourceSceneKey),
		strings.TrimSpace(token.GetUpdatesBuf),
		string(contextJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("upsert weixin clawbot token: %w", err)
	}
	id, _ := res.LastInsertId()
	if id > 0 {
		return a.GetWeixinClawBotTokenByID(id)
	}
	return a.GetWeixinClawBotTokenByHash(token.TokenHash)
}

func (a *Adapter) GetWeixinClawBotTokenByID(id int64) (*types.WeixinClawBotToken, error) {
	if id <= 0 {
		return nil, nil
	}
	row := a.db.QueryRow(`SELECT `+weixinClawBotTokenColumns+` FROM weixin_clawbot_tokens WHERE id = ?`, id)
	token, err := scanWeixinClawBotToken(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get weixin clawbot token: %w", err)
	}
	return token, nil
}

func (a *Adapter) GetWeixinClawBotTokenByHash(tokenHash string) (*types.WeixinClawBotToken, error) {
	tokenHash = strings.TrimSpace(tokenHash)
	if tokenHash == "" {
		return nil, nil
	}
	row := a.db.QueryRow(`SELECT `+weixinClawBotTokenColumns+` FROM weixin_clawbot_tokens WHERE token_hash = ?`, tokenHash)
	token, err := scanWeixinClawBotToken(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get weixin clawbot token by hash: %w", err)
	}
	return token, nil
}

func (a *Adapter) ListActiveWeixinClawBotTokens() ([]*types.WeixinClawBotToken, error) {
	rows, err := a.db.Query(
		`SELECT `+weixinClawBotTokenColumns+`
		 FROM weixin_clawbot_tokens
		 WHERE status = ?
		 ORDER BY updated_at ASC, id ASC`,
		types.WeixinClawBotTokenActive,
	)
	if err != nil {
		return nil, fmt.Errorf("list active weixin clawbot tokens: %w", err)
	}
	defer rows.Close()
	var tokens []*types.WeixinClawBotToken
	for rows.Next() {
		token, scanErr := scanWeixinClawBotToken(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

func (a *Adapter) UpdateWeixinClawBotTokenPollState(id int64, getUpdatesBuf string, contextTokens map[string]types.WeixinClawBotContext) error {
	if id <= 0 {
		return nil
	}
	contextJSON, err := json.Marshal(normalizeWeixinClawBotContexts(contextTokens))
	if err != nil {
		return err
	}
	_, err = a.db.Exec(
		`UPDATE weixin_clawbot_tokens
		 SET get_updates_buf = ?, context_tokens = ?, last_poll_at = CURRENT_TIMESTAMP,
		     last_error = '', last_error_at = NULL, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		strings.TrimSpace(getUpdatesBuf), string(contextJSON), id,
	)
	if err != nil {
		return fmt.Errorf("update weixin clawbot poll state: %w", err)
	}
	return nil
}

func (a *Adapter) MarkWeixinClawBotTokenError(id int64, status string, message string) error {
	if id <= 0 {
		return nil
	}
	message = truncateWeixinClawBotError(message)
	status = strings.TrimSpace(status)
	var err error
	if status == "" {
		_, err = a.db.Exec(
			`UPDATE weixin_clawbot_tokens SET last_error = ?, last_error_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			message, id,
		)
	} else {
		_, err = a.db.Exec(
			`UPDATE weixin_clawbot_tokens SET status = ?, last_error = ?, last_error_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			status, message, id,
		)
	}
	if err != nil {
		return fmt.Errorf("mark weixin clawbot token error: %w", err)
	}
	return nil
}

type weixinClawBotTokenScanner interface {
	Scan(dest ...interface{}) error
}

func scanWeixinClawBotToken(row weixinClawBotTokenScanner) (*types.WeixinClawBotToken, error) {
	var token types.WeixinClawBotToken
	var contextRaw []byte
	var lastPollAt, lastUsedAt, lastErrorAt sql.NullTime
	if err := row.Scan(
		&token.ID,
		&token.TokenHash,
		&token.BotToken,
		&token.TokenLast4,
		&token.Status,
		&token.OwnerUID,
		&token.ILinkBotID,
		&token.ILinkUserID,
		&token.BaseURL,
		&token.SourceSceneKey,
		&token.GetUpdatesBuf,
		&contextRaw,
		&lastPollAt,
		&lastUsedAt,
		&lastErrorAt,
		&token.LastError,
		&token.CreatedAt,
		&token.UpdatedAt,
	); err != nil {
		return nil, err
	}
	token.ContextTokens = parseWeixinClawBotContexts(contextRaw)
	if lastPollAt.Valid {
		token.LastPollAt = &lastPollAt.Time
	}
	if lastUsedAt.Valid {
		token.LastUsedAt = &lastUsedAt.Time
	}
	if lastErrorAt.Valid {
		token.LastErrorAt = &lastErrorAt.Time
	}
	return &token, nil
}

func normalizeWeixinClawBotContexts(contexts map[string]types.WeixinClawBotContext) map[string]types.WeixinClawBotContext {
	if contexts == nil {
		return map[string]types.WeixinClawBotContext{}
	}
	next := make(map[string]types.WeixinClawBotContext, len(contexts))
	for key, value := range contexts {
		key = strings.TrimSpace(key)
		value.ContextToken = strings.TrimSpace(value.ContextToken)
		value.BotUserID = strings.TrimSpace(value.BotUserID)
		if key == "" || value.ContextToken == "" {
			continue
		}
		if value.UpdatedAt.IsZero() {
			value.UpdatedAt = time.Now()
		}
		next[key] = value
	}
	return next
}

func parseWeixinClawBotContexts(raw []byte) map[string]types.WeixinClawBotContext {
	if len(raw) == 0 {
		return map[string]types.WeixinClawBotContext{}
	}
	var contexts map[string]types.WeixinClawBotContext
	if err := json.Unmarshal(raw, &contexts); err != nil || contexts == nil {
		return map[string]types.WeixinClawBotContext{}
	}
	return normalizeWeixinClawBotContexts(contexts)
}

func truncateWeixinClawBotError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= 1000 {
		return message
	}
	return message[:1000]
}
