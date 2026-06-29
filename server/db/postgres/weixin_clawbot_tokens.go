package postgres

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openchat/openchat/server/store/types"
)

const weixinClawBotTokenColumns = `
	id, token_hash, bot_token, token_last4, status, owner_uid, COALESCE(agent_uid, 0),
	COALESCE(entry_id, 0), canonical_uid, COALESCE(group_id, 0), topic_id,
	source_scene_key, get_updates_buf, context_tokens, last_poll_at, last_used_at,
	last_error_at, last_error, created_at, updated_at`

func (a *Adapter) UpsertWeixinClawBotToken(token *types.WeixinClawBotToken) (*types.WeixinClawBotToken, error) {
	if token == nil || strings.TrimSpace(token.TokenHash) == "" || strings.TrimSpace(token.BotToken) == "" || token.OwnerUID <= 0 || token.CanonicalUID <= 0 {
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
	var id int64
	if err := a.db.QueryRow(
		`INSERT INTO weixin_clawbot_tokens (
		     token_hash, bot_token, token_last4, status, owner_uid, agent_uid, entry_id,
		     canonical_uid, group_id, topic_id, source_scene_key, get_updates_buf, context_tokens
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb)
		 ON CONFLICT (token_hash) DO UPDATE SET
		     bot_token = EXCLUDED.bot_token,
		     token_last4 = EXCLUDED.token_last4,
		     status = EXCLUDED.status,
		     owner_uid = EXCLUDED.owner_uid,
		     agent_uid = EXCLUDED.agent_uid,
		     entry_id = EXCLUDED.entry_id,
		     canonical_uid = EXCLUDED.canonical_uid,
		     group_id = EXCLUDED.group_id,
		     topic_id = EXCLUDED.topic_id,
		     source_scene_key = EXCLUDED.source_scene_key,
		     context_tokens = CASE WHEN EXCLUDED.context_tokens = '{}'::jsonb THEN weixin_clawbot_tokens.context_tokens ELSE EXCLUDED.context_tokens END,
		     last_error = '',
		     last_error_at = NULL
		 RETURNING id`,
		strings.TrimSpace(token.TokenHash),
		strings.TrimSpace(token.BotToken),
		strings.TrimSpace(token.TokenLast4),
		status,
		token.OwnerUID,
		pgNullableInt64(token.AgentUID),
		pgNullableInt64(token.EntryID),
		token.CanonicalUID,
		pgNullableInt64(token.GroupID),
		strings.TrimSpace(token.TopicID),
		strings.TrimSpace(token.SourceSceneKey),
		strings.TrimSpace(token.GetUpdatesBuf),
		string(contextJSON),
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("upsert weixin clawbot token: %w", err)
	}
	return a.GetWeixinClawBotTokenByID(id)
}

func (a *Adapter) GetWeixinClawBotTokenByID(id int64) (*types.WeixinClawBotToken, error) {
	if id <= 0 {
		return nil, nil
	}
	row := a.db.QueryRow(`SELECT `+weixinClawBotTokenColumns+` FROM weixin_clawbot_tokens WHERE id = $1`, id)
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
	row := a.db.QueryRow(`SELECT `+weixinClawBotTokenColumns+` FROM weixin_clawbot_tokens WHERE token_hash = $1`, tokenHash)
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
		 WHERE status = $1
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
		 SET get_updates_buf = $2, context_tokens = $3::jsonb, last_poll_at = CURRENT_TIMESTAMP,
		     last_error = '', last_error_at = NULL
		 WHERE id = $1`,
		id, strings.TrimSpace(getUpdatesBuf), string(contextJSON),
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
			`UPDATE weixin_clawbot_tokens SET last_error = $2, last_error_at = CURRENT_TIMESTAMP WHERE id = $1`,
			id, message,
		)
	} else {
		_, err = a.db.Exec(
			`UPDATE weixin_clawbot_tokens SET status = $2, last_error = $3, last_error_at = CURRENT_TIMESTAMP WHERE id = $1`,
			id, status, message,
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
		&token.AgentUID,
		&token.EntryID,
		&token.CanonicalUID,
		&token.GroupID,
		&token.TopicID,
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

func pgNullableInt64(value int64) interface{} {
	if value <= 0 {
		return nil
	}
	return value
}
