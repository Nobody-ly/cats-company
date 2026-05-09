package mysql

import (
	"database/sql"
	"fmt"

	"github.com/openchat/openchat/server/store/types"
)

// SaveWeComConfig saves or updates one Enterprise WeChat app binding for a bot.
func (a *Adapter) SaveWeComConfig(botUID int64, cfg *types.WeComConfig) error {
	if cfg == nil {
		return fmt.Errorf("wecom config is nil")
	}
	_, err := a.db.Exec(
		`INSERT INTO wecom_integrations
		 (bot_uid, corp_id, agent_id, app_secret, callback_token, encoding_aes_key, api_base_url, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1)
		 ON DUPLICATE KEY UPDATE
		   corp_id = VALUES(corp_id),
		   agent_id = VALUES(agent_id),
		   app_secret = VALUES(app_secret),
		   callback_token = VALUES(callback_token),
		   encoding_aes_key = VALUES(encoding_aes_key),
		   api_base_url = VALUES(api_base_url),
		   enabled = 1,
		   updated_at = CURRENT_TIMESTAMP`,
		botUID,
		cfg.CorpID,
		cfg.AgentID,
		cfg.Secret,
		cfg.CallbackToken,
		cfg.EncodingAESKey,
		cfg.APIBaseURL,
	)
	return err
}

// GetWeComConfig returns the Enterprise WeChat app binding for a bot.
func (a *Adapter) GetWeComConfig(botUID int64) (*types.WeComConfig, error) {
	cfg := &types.WeComConfig{}
	err := a.db.QueryRow(
		`SELECT bot_uid, corp_id, agent_id, app_secret, callback_token, encoding_aes_key,
		        api_base_url, enabled, created_at, updated_at
		 FROM wecom_integrations WHERE bot_uid = ? AND enabled = 1`,
		botUID,
	).Scan(
		&cfg.BotUID,
		&cfg.CorpID,
		&cfg.AgentID,
		&cfg.Secret,
		&cfg.CallbackToken,
		&cfg.EncodingAESKey,
		&cfg.APIBaseURL,
		&cfg.Enabled,
		&cfg.CreatedAt,
		&cfg.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wecom config: %w", err)
	}
	return cfg, nil
}
