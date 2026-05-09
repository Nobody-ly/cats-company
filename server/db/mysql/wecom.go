package mysql

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/openchat/openchat/server/store/types"
)

func (a *Adapter) SaveWeComSuiteTicket(suiteID, ticket string) error {
	_, err := a.db.Exec(
		`INSERT INTO wecom_suite_state (suite_id, suite_ticket)
		 VALUES (?, ?)
		 ON DUPLICATE KEY UPDATE
		   suite_ticket = VALUES(suite_ticket),
		   updated_at = CURRENT_TIMESTAMP`,
		suiteID,
		ticket,
	)
	return err
}

func (a *Adapter) GetWeComSuiteState(suiteID string) (*types.WeComSuiteState, error) {
	state := &types.WeComSuiteState{}
	err := a.db.QueryRow(
		`SELECT suite_id, COALESCE(suite_ticket, ''), COALESCE(suite_access_token, ''),
		        suite_access_token_expires_at, updated_at
		 FROM wecom_suite_state WHERE suite_id = ?`,
		suiteID,
	).Scan(
		&state.SuiteID,
		&state.SuiteTicket,
		&state.SuiteAccessToken,
		&state.SuiteAccessTokenExpiresAt,
		&state.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wecom suite state: %w", err)
	}
	return state, nil
}

func (a *Adapter) SaveWeComSuiteToken(suiteID, token string, expiresAt time.Time) error {
	_, err := a.db.Exec(
		`INSERT INTO wecom_suite_state (suite_id, suite_access_token, suite_access_token_expires_at)
		 VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   suite_access_token = VALUES(suite_access_token),
		   suite_access_token_expires_at = VALUES(suite_access_token_expires_at),
		   updated_at = CURRENT_TIMESTAMP`,
		suiteID,
		token,
		expiresAt,
	)
	return err
}

func (a *Adapter) SaveWeComSuiteAuth(auth *types.WeComSuiteAuth) error {
	if auth == nil {
		return fmt.Errorf("wecom suite auth is nil")
	}
	_, err := a.db.Exec(
		`INSERT INTO wecom_suite_auths
		 (auth_corp_id, agent_id, auth_corp_name, permanent_code, bot_uid, enabled)
		 VALUES (?, ?, ?, ?, NULLIF(?, 0), 1)
		 ON DUPLICATE KEY UPDATE
		   auth_corp_name = VALUES(auth_corp_name),
		   permanent_code = VALUES(permanent_code),
		   bot_uid = COALESCE(VALUES(bot_uid), bot_uid),
		   enabled = 1,
		   updated_at = CURRENT_TIMESTAMP`,
		auth.AuthCorpID,
		auth.AgentID,
		auth.AuthCorpName,
		auth.PermanentCode,
		auth.BotUID,
	)
	return err
}

func (a *Adapter) BindWeComSuiteAuth(botUID int64, authCorpID, agentID string) error {
	result, err := a.db.Exec(
		`UPDATE wecom_suite_auths
		 SET bot_uid = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE auth_corp_id = ? AND agent_id = ? AND enabled = 1`,
		botUID,
		authCorpID,
		agentID,
	)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (a *Adapter) GetWeComSuiteAuth(authCorpID, agentID string) (*types.WeComSuiteAuth, error) {
	return a.scanWeComSuiteAuth(
		`SELECT auth_corp_id, agent_id, auth_corp_name, permanent_code, COALESCE(bot_uid, 0),
		        enabled, created_at, updated_at
		 FROM wecom_suite_auths
		 WHERE auth_corp_id = ? AND agent_id = ? AND enabled = 1`,
		authCorpID,
		agentID,
	)
}

func (a *Adapter) GetWeComSuiteAuthByBotUID(botUID int64) (*types.WeComSuiteAuth, error) {
	return a.scanWeComSuiteAuth(
		`SELECT auth_corp_id, agent_id, auth_corp_name, permanent_code, COALESCE(bot_uid, 0),
		        enabled, created_at, updated_at
		 FROM wecom_suite_auths
		 WHERE bot_uid = ? AND enabled = 1
		 ORDER BY updated_at DESC LIMIT 1`,
		botUID,
	)
}

func (a *Adapter) scanWeComSuiteAuth(query string, args ...interface{}) (*types.WeComSuiteAuth, error) {
	auth := &types.WeComSuiteAuth{}
	err := a.db.QueryRow(query, args...).Scan(
		&auth.AuthCorpID,
		&auth.AgentID,
		&auth.AuthCorpName,
		&auth.PermanentCode,
		&auth.BotUID,
		&auth.Enabled,
		&auth.CreatedAt,
		&auth.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get wecom suite auth: %w", err)
	}
	return auth, nil
}
