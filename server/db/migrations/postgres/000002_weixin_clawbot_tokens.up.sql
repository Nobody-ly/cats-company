CREATE TABLE IF NOT EXISTS weixin_clawbot_tokens (
    id BIGSERIAL PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    bot_token TEXT NOT NULL,
    token_last4 VARCHAR(8) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    owner_uid BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    agent_uid BIGINT DEFAULT NULL REFERENCES users(id) ON DELETE CASCADE,
    entry_id BIGINT DEFAULT NULL REFERENCES channel_agent_entries(id) ON DELETE SET NULL,
    canonical_uid BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id BIGINT DEFAULT NULL REFERENCES "groups"(id) ON DELETE CASCADE,
    topic_id VARCHAR(128) NOT NULL DEFAULT '',
    source_scene_key VARCHAR(64) NOT NULL DEFAULT '',
    get_updates_buf TEXT NOT NULL DEFAULT '',
    context_tokens JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_poll_at TIMESTAMPTZ DEFAULT NULL,
    last_used_at TIMESTAMPTZ DEFAULT NULL,
    last_error_at TIMESTAMPTZ DEFAULT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_weixin_clawbot_tokens_active ON weixin_clawbot_tokens (status, updated_at);
CREATE INDEX IF NOT EXISTS idx_weixin_clawbot_tokens_agent ON weixin_clawbot_tokens (agent_uid, canonical_uid, status);
CREATE INDEX IF NOT EXISTS idx_weixin_clawbot_tokens_group ON weixin_clawbot_tokens (group_id, topic_id, status);

CREATE OR REPLACE TRIGGER trg_weixin_clawbot_tokens_updated_at BEFORE UPDATE ON weixin_clawbot_tokens
FOR EACH ROW EXECUTE FUNCTION set_updated_at();
