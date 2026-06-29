package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openchat/openchat/server/store"
	"github.com/openchat/openchat/server/store/types"
)

const weixinClawBotChannelVersion = "catsco-weixin-clawbot/1.0"

type WeixinClawBotConfig struct {
	ILinkBaseURL    string
	WorkerEnabled   bool
	RefreshInterval time.Duration
	LongPollTimeout time.Duration
}

type WeixinClawBotHandler struct {
	db     store.Store
	hub    *Hub
	config WeixinClawBotConfig
	api    weixinClawBotAPI

	mu      sync.Mutex
	cancel  context.CancelFunc
	running map[int64]context.CancelFunc
}

type weixinClawBotAPI interface {
	GetQRCodeStatus(ctx context.Context, qrcode string) (*weixinClawBotQRCodeStatus, error)
	GetUpdates(ctx context.Context, botToken string, getUpdatesBuf string) (*weixinClawBotUpdates, error)
	SendTextMessage(ctx context.Context, botToken string, toUserID string, text string, contextToken string, fromUserID string) error
}

type weixinClawBotQRCodeStatus struct {
	Ret         int    `json:"ret,omitempty"`
	ErrCode     int    `json:"errcode,omitempty"`
	ErrMsg      string `json:"errmsg,omitempty"`
	Status      string `json:"status,omitempty"`
	BotToken    string `json:"bot_token,omitempty"`
	ILinkBotID  string `json:"ilink_bot_id,omitempty"`
	ILinkUserID string `json:"ilink_user_id,omitempty"`
	BaseURL     string `json:"baseurl,omitempty"`
}

type weixinClawBotUpdates struct {
	Ret           int                    `json:"ret,omitempty"`
	ErrCode       int                    `json:"errcode,omitempty"`
	ErrMsg        string                 `json:"errmsg,omitempty"`
	Messages      []weixinClawBotMessage `json:"msgs,omitempty"`
	GetUpdatesBuf string                 `json:"get_updates_buf,omitempty"`
}

type weixinClawBotMessage struct {
	MessageID    json.RawMessage            `json:"message_id,omitempty"`
	MessageType  int                        `json:"message_type,omitempty"`
	FromUserID   string                     `json:"from_user_id,omitempty"`
	ToUserID     string                     `json:"to_user_id,omitempty"`
	ContextToken string                     `json:"context_token,omitempty"`
	ItemList     []weixinClawBotMessageItem `json:"item_list,omitempty"`
}

type weixinClawBotMessageItem struct {
	Type     int `json:"type,omitempty"`
	TextItem struct {
		Text string `json:"text,omitempty"`
	} `json:"text_item,omitempty"`
}

type weixinClawBotAPIError struct {
	Operation string
	Status    int
	Ret       int
	ErrCode   int
	ErrMsg    string
}

func (e *weixinClawBotAPIError) Error() string {
	if e == nil {
		return ""
	}
	if e.ErrMsg != "" {
		return fmt.Sprintf("weixin clawbot %s failed: %s", e.Operation, e.ErrMsg)
	}
	if e.Status > 0 {
		return fmt.Sprintf("weixin clawbot %s failed: http status %d", e.Operation, e.Status)
	}
	return fmt.Sprintf("weixin clawbot %s failed: ret=%d errcode=%d", e.Operation, e.Ret, e.ErrCode)
}

func NewWeixinClawBotHandlerFromEnv(db store.Store, hub *Hub) *WeixinClawBotHandler {
	cfg := weixinClawBotConfigFromEnv()
	return NewWeixinClawBotHandler(db, hub, cfg, nil)
}

func NewWeixinClawBotHandler(db store.Store, hub *Hub, cfg WeixinClawBotConfig, api weixinClawBotAPI) *WeixinClawBotHandler {
	if strings.TrimSpace(cfg.ILinkBaseURL) == "" {
		cfg.ILinkBaseURL = configuredWeixinClawBotILinkBaseURL()
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 10 * time.Second
	}
	if cfg.LongPollTimeout <= 0 {
		cfg.LongPollTimeout = 30 * time.Second
	}
	if api == nil {
		api = newHTTPWeixinClawBotAPI(cfg)
	}
	return &WeixinClawBotHandler{
		db:      db,
		hub:     hub,
		config:  cfg,
		api:     api,
		running: map[int64]context.CancelFunc{},
	}
}

func weixinClawBotConfigFromEnv() WeixinClawBotConfig {
	return WeixinClawBotConfig{
		ILinkBaseURL:    configuredWeixinClawBotILinkBaseURL(),
		WorkerEnabled:   !falseEnv(firstEnv("CATSCO_WEIXIN_CLAWBOT_WORKER_ENABLED", "WEIXIN_CLAWBOT_WORKER_ENABLED")),
		RefreshInterval: secondsEnvDuration("CATSCO_WEIXIN_CLAWBOT_WORKER_REFRESH_SECONDS", 10, 2, 120),
		LongPollTimeout: secondsEnvDuration("CATSCO_WEIXIN_CLAWBOT_LONGPOLL_SECONDS", 30, 5, 120),
	}
}

func (h *WeixinClawBotHandler) InstallOutboundDispatcher() {
	if h == nil || h.hub == nil {
		return
	}
	h.hub.mu.Lock()
	defer h.hub.mu.Unlock()
	if h.hub.channelOut == nil {
		h.hub.channelOut = NewChannelOutboundDispatcher(h.db, nil, "").WithWeixinClawBot(h)
		return
	}
	h.hub.channelOut.WithWeixinClawBot(h)
}

func (h *WeixinClawBotHandler) Start() {
	if h == nil || !h.config.WorkerEnabled {
		return
	}
	h.mu.Lock()
	if h.cancel != nil {
		h.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.mu.Unlock()
	go h.manageTokenWorkers(ctx)
}

func (h *WeixinClawBotHandler) Stop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	cancel := h.cancel
	h.cancel = nil
	for id, stop := range h.running {
		stop()
		delete(h.running, id)
	}
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (h *WeixinClawBotHandler) HandleQRCodeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	qrcode := strings.TrimSpace(r.URL.Query().Get("qrcode"))
	sceneKey := strings.TrimSpace(r.URL.Query().Get("scene_key"))
	if qrcode == "" || sceneKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing qrcode or scene_key"})
		return
	}
	status, err := h.api.GetQRCodeStatus(r.Context(), qrcode)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]interface{}{
		"ret":            status.Ret,
		"errcode":        status.ErrCode,
		"errmsg":         status.ErrMsg,
		"status":         status.Status,
		"token_received": strings.TrimSpace(status.BotToken) != "",
	}
	if strings.EqualFold(strings.TrimSpace(status.Status), "confirmed") && strings.TrimSpace(status.BotToken) != "" {
		token, target, saveErr := h.saveAuthorizedTokenForScene(r, sceneKey, status)
		if saveErr != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": saveErr.Error()})
			return
		}
		resp["token_saved"] = true
		resp["target"] = target
		resp["token"] = token
		if h.config.WorkerEnabled {
			h.syncTokenWorkers(context.Background())
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *WeixinClawBotHandler) saveAuthorizedTokenForScene(r *http.Request, sceneKey string, status *weixinClawBotQRCodeStatus) (*types.WeixinClawBotToken, string, error) {
	bindings, ok := h.db.(store.ChannelAgentBindingStore)
	if !ok {
		return nil, "", errors.New("channel binding store not configured")
	}
	canonicalUID := UIDFromContext(r.Context())
	if canonicalUID <= 0 {
		return nil, "", errors.New("login required")
	}
	if status == nil || strings.TrimSpace(status.BotToken) == "" {
		return nil, "", errors.New("ClawBot authorization did not return bot_token")
	}
	channelUserID := strings.TrimSpace(status.ILinkUserID)
	if channelUserID == "" {
		return nil, "", errors.New("ClawBot authorization did not return ilink_user_id")
	}
	botToken := strings.TrimSpace(status.BotToken)
	tokenHash := hashWeixinClawBotToken(botToken)
	token := &types.WeixinClawBotToken{
		TokenHash:      tokenHash,
		BotToken:       botToken,
		TokenLast4:     last4(botToken),
		Status:         types.WeixinClawBotTokenActive,
		ILinkBotID:     strings.TrimSpace(status.ILinkBotID),
		ILinkUserID:    channelUserID,
		BaseURL:        strings.TrimSpace(status.BaseURL),
		SourceSceneKey: sceneKey,
		ContextTokens:  map[string]types.WeixinClawBotContext{},
	}
	if strings.HasPrefix(sceneKey, "m.") {
		link, err := bindings.GetChannelIdentityMobileLink(sceneKey)
		if err != nil || link == nil {
			return nil, "", errors.New("ClawBot mobile link not found or expired")
		}
		if link.CanonicalUID != canonicalUID {
			return nil, "", errors.New("ClawBot mobile link belongs to another CatsCo user")
		}
		entry, resolvedCanonicalUID, err := resolveChannelIdentityMobileLink(h.db, sceneKey, "weixin_clawbot", "", false)
		if err != nil {
			return nil, "", err
		}
		if entry == nil || resolvedCanonicalUID != canonicalUID {
			return nil, "", errors.New("ClawBot mobile link is no longer valid")
		}
		token.OwnerUID = entry.OwnerUID
		saved, err := bindings.UpsertWeixinClawBotToken(token)
		if err != nil {
			return nil, "", err
		}
		actorUID, err := ensureChannelActor(h.db, "weixin_clawbot", tokenHash, channelUserID)
		if err != nil {
			return nil, "", err
		}
		binding, _, err := bindOrRequestChannelAgentAccessWithCanonical(
			h.db,
			bindings,
			entry,
			actorUID,
			"weixin_clawbot",
			tokenHash,
			channelUserID,
			"",
			"p2p",
			canonicalUID,
		)
		if err != nil {
			return nil, "", err
		}
		if binding == nil {
			return nil, "", errors.New("ClawBot binding was not created")
		}
		_, _, _ = resolveChannelIdentityMobileLink(h.db, sceneKey, "weixin_clawbot", "", true)
		return saved, "agent", nil
	}
	if strings.HasPrefix(sceneKey, "g.") {
		link, group, err := resolveChannelGroupMobileLink(h.db, sceneKey, "weixin_clawbot", "", false)
		if err != nil {
			return nil, "", err
		}
		if link == nil || link.CanonicalUID != canonicalUID {
			return nil, "", errors.New("ClawBot group mobile link not found or expired")
		}
		ownerUID := link.CanonicalUID
		if group != nil && group.OwnerID > 0 {
			ownerUID = group.OwnerID
		}
		token.OwnerUID = ownerUID
		saved, err := bindings.UpsertWeixinClawBotToken(token)
		if err != nil {
			return nil, "", err
		}
		actorUID, err := ensureChannelActor(h.db, "weixin_clawbot", tokenHash, channelUserID)
		if err != nil {
			return nil, "", err
		}
		if _, err := bindings.UpsertChannelGroupBinding(&types.ChannelGroupBinding{
			Channel:                 "weixin_clawbot",
			ChannelAppID:            tokenHash,
			ChannelUserID:           channelUserID,
			ChannelConversationType: "p2p",
			ActorUID:                actorUID,
			CanonicalUID:            canonicalUID,
			GroupID:                 link.GroupID,
			TopicID:                 link.TopicID,
			Status:                  types.ChannelAgentBindingActive,
		}); err != nil {
			return nil, "", err
		}
		_, _, _ = resolveChannelGroupMobileLink(h.db, sceneKey, "weixin_clawbot", "", true)
		return saved, "group", nil
	}
	return nil, "", errors.New("unsupported ClawBot mobile link")
}

func (h *WeixinClawBotHandler) SendTextMessage(ctx context.Context, binding *types.ChannelAgentBinding, text string) error {
	if h == nil || h.db == nil || binding == nil {
		return nil
	}
	tokenHash := strings.TrimSpace(binding.ChannelAppID)
	channelUserID := strings.TrimSpace(binding.ChannelUserID)
	if tokenHash == "" || channelUserID == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	bindings, ok := h.db.(store.ChannelAgentBindingStore)
	if !ok {
		return nil
	}
	token, err := bindings.GetWeixinClawBotTokenByHash(tokenHash)
	if err != nil || token == nil || token.Status != types.WeixinClawBotTokenActive {
		return err
	}
	contextValue := token.ContextTokens[channelUserID]
	if strings.TrimSpace(contextValue.ContextToken) == "" {
		return fmt.Errorf("missing weixin clawbot context token for channel user")
	}
	return h.api.SendTextMessage(ctx, token.BotToken, channelUserID, text, contextValue.ContextToken, contextValue.BotUserID)
}

func (h *WeixinClawBotHandler) manageTokenWorkers(ctx context.Context) {
	h.syncTokenWorkers(ctx)
	ticker := time.NewTicker(h.config.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			h.Stop()
			return
		case <-ticker.C:
			h.syncTokenWorkers(ctx)
		}
	}
}

func (h *WeixinClawBotHandler) syncTokenWorkers(ctx context.Context) {
	if h == nil || h.db == nil {
		return
	}
	bindings, ok := h.db.(store.ChannelAgentBindingStore)
	if !ok {
		return
	}
	tokens, err := bindings.ListActiveWeixinClawBotTokens()
	if err != nil {
		log.Printf("list weixin clawbot tokens failed: %v", err)
		return
	}
	active := map[int64]*types.WeixinClawBotToken{}
	for _, token := range tokens {
		if token != nil && token.ID > 0 {
			active[token.ID] = token
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, cancel := range h.running {
		if _, ok := active[id]; !ok {
			cancel()
			delete(h.running, id)
		}
	}
	for id := range active {
		if _, ok := h.running[id]; ok {
			continue
		}
		tokenCtx, cancel := context.WithCancel(ctx)
		h.running[id] = cancel
		go h.pollTokenLoop(tokenCtx, id)
	}
}

func (h *WeixinClawBotHandler) pollTokenLoop(ctx context.Context, tokenID int64) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		bindings, ok := h.db.(store.ChannelAgentBindingStore)
		if !ok {
			return
		}
		token, err := bindings.GetWeixinClawBotTokenByID(tokenID)
		if err != nil {
			log.Printf("load weixin clawbot token failed id=%d: %v", tokenID, err)
			sleepWithContext(ctx, backoff)
			backoff = nextBackoff(backoff)
			continue
		}
		if token == nil || token.Status != types.WeixinClawBotTokenActive {
			return
		}
		if err := h.pollTokenOnce(ctx, token); err != nil {
			var apiErr *weixinClawBotAPIError
			if errors.As(err, &apiErr) && apiErr.ErrCode == -14 {
				_ = bindings.MarkWeixinClawBotTokenError(token.ID, types.WeixinClawBotTokenExpired, err.Error())
				return
			}
			_ = bindings.MarkWeixinClawBotTokenError(token.ID, "", err.Error())
			log.Printf("poll weixin clawbot token failed id=%d hash=%s: %v", token.ID, token.TokenHash, err)
			sleepWithContext(ctx, backoff)
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = time.Second
	}
}

func (h *WeixinClawBotHandler) pollTokenOnce(ctx context.Context, token *types.WeixinClawBotToken) error {
	if token == nil || token.ID <= 0 || strings.TrimSpace(token.BotToken) == "" {
		return nil
	}
	updates, err := h.api.GetUpdates(ctx, token.BotToken, token.GetUpdatesBuf)
	if err != nil {
		return err
	}
	if updates.GetUpdatesBuf != "" {
		token.GetUpdatesBuf = updates.GetUpdatesBuf
	}
	contexts := copyWeixinClawBotContexts(token.ContextTokens)
	for _, msg := range updates.Messages {
		fromUserID := strings.TrimSpace(msg.FromUserID)
		if fromUserID != "" && strings.TrimSpace(msg.ContextToken) != "" {
			contexts[fromUserID] = types.WeixinClawBotContext{
				ContextToken: strings.TrimSpace(msg.ContextToken),
				BotUserID:    strings.TrimSpace(msg.ToUserID),
				UpdatedAt:    time.Now(),
			}
		}
	}
	bindings, ok := h.db.(store.ChannelAgentBindingStore)
	if !ok {
		return nil
	}
	if err := bindings.UpdateWeixinClawBotTokenPollState(token.ID, token.GetUpdatesBuf, contexts); err != nil {
		return err
	}
	for _, msg := range updates.Messages {
		if err := h.handleUpdateMessage(ctx, token, msg); err != nil {
			log.Printf("handle weixin clawbot message failed token=%d message=%s: %v", token.ID, clawBotMessageID(msg), err)
		}
	}
	return nil
}

func (h *WeixinClawBotHandler) handleUpdateMessage(ctx context.Context, token *types.WeixinClawBotToken, msg weixinClawBotMessage) error {
	if msg.MessageType == 2 {
		return nil
	}
	if msg.MessageType != 0 && msg.MessageType != 1 {
		return nil
	}
	fromUserID := strings.TrimSpace(msg.FromUserID)
	if fromUserID == "" {
		return nil
	}
	text := strings.TrimSpace(weixinClawBotMessageText(msg))
	if text == "" {
		return nil
	}
	if delivered, err := h.deliverGroupMessage(ctx, token, msg, text); delivered || err != nil {
		return err
	}
	return h.deliverAgentMessage(ctx, token, msg, text)
}

func (h *WeixinClawBotHandler) deliverAgentMessage(ctx context.Context, token *types.WeixinClawBotToken, msg weixinClawBotMessage, text string) error {
	bindings, ok := h.db.(store.ChannelAgentBindingStore)
	if !ok {
		return errors.New("channel binding store not configured")
	}
	binding, err := bindings.ResolveChannelAgentBinding(types.ChannelAgentBindingQuery{
		Channel:                 "weixin_clawbot",
		ChannelAppID:            token.TokenHash,
		ChannelUserID:           strings.TrimSpace(msg.FromUserID),
		ChannelConversationType: "p2p",
	})
	if err != nil {
		return err
	}
	if binding == nil {
		return errors.New("weixin clawbot message user is not bound to an agent")
	}
	if _, err := bindings.UpsertChannelAgentRoute(&types.ChannelAgentRoute{
		Channel:                 "weixin_clawbot",
		ChannelAppID:            token.TokenHash,
		ChannelUserID:           strings.TrimSpace(msg.FromUserID),
		ChannelConversationType: "p2p",
		ActorUID:                binding.ActorUID,
		AgentUID:                binding.AgentUID,
		Source:                  "weixin_clawbot_getupdates",
	}); err != nil {
		return err
	}
	return deliverInboundChannelTextToAgent(
		h.db,
		h.hub,
		binding.ActorUID,
		binding.AgentUID,
		text,
		weixinClawBotClientMsgID(token, msg),
		"weixin_clawbot",
		h.inboundMetadata(token, binding, msg),
	)
}

func (h *WeixinClawBotHandler) deliverGroupMessage(ctx context.Context, token *types.WeixinClawBotToken, msg weixinClawBotMessage, text string) (bool, error) {
	bindings, ok := h.db.(store.ChannelAgentBindingStore)
	if !ok {
		return false, errors.New("channel binding store not configured")
	}
	groupBinding, err := bindings.ResolveChannelGroupBinding(types.ChannelGroupBindingQuery{
		Channel:                 "weixin_clawbot",
		ChannelAppID:            token.TokenHash,
		ChannelUserID:           strings.TrimSpace(msg.FromUserID),
		ChannelConversationType: "p2p",
	})
	if err != nil {
		return false, err
	}
	if groupBinding == nil || groupBinding.Status != types.ChannelAgentBindingActive {
		return false, nil
	}
	if err := deliverInboundChannelTextToGroup(
		h.db,
		h.hub,
		groupBinding.CanonicalUID,
		groupBinding,
		text,
		weixinClawBotClientMsgID(token, msg),
		"weixin_clawbot",
		h.groupInboundMetadata(token, groupBinding, msg),
	); err != nil {
		return true, err
	}
	return true, nil
}

func (h *WeixinClawBotHandler) inboundMetadata(token *types.WeixinClawBotToken, binding *types.ChannelAgentBinding, msg weixinClawBotMessage) map[string]interface{} {
	return map[string]interface{}{
		"source_channel":                 "weixin_clawbot",
		"channel_app_id":                 token.TokenHash,
		"channel_user_id":                strings.TrimSpace(msg.FromUserID),
		"channel_conversation_type":      "p2p",
		"channel_message_id":             clawBotMessageID(msg),
		"channel_message_type":           msg.MessageType,
		"channel_identity_source":        "weixin.ilink",
		"channel_identity_trust":         "weixin_clawbot_bot_token",
		"channel_agent_binding_entry_id": binding.EntryID,
		"channel_actor_uid":              binding.ActorUID,
		"channel_canonical_uid":          binding.CanonicalUID,
		"channel_agent_binding_id":       binding.ID,
		"channel_device_access_enabled":  binding.DeviceAccessEnabled,
		"weixin_clawbot_token_id":        token.ID,
		"weixin_clawbot_bot_user_id":     strings.TrimSpace(msg.ToUserID),
	}
}

func (h *WeixinClawBotHandler) groupInboundMetadata(token *types.WeixinClawBotToken, binding *types.ChannelGroupBinding, msg weixinClawBotMessage) map[string]interface{} {
	return map[string]interface{}{
		"source_channel":                    "weixin_clawbot",
		"channel_app_id":                    token.TokenHash,
		"channel_user_id":                   strings.TrimSpace(msg.FromUserID),
		"channel_conversation_type":         "p2p",
		"channel_message_id":                clawBotMessageID(msg),
		"channel_message_type":              msg.MessageType,
		"channel_identity_source":           "weixin.ilink",
		"channel_identity_trust":            "weixin_clawbot_bot_token",
		"channel_group_binding_id":          binding.ID,
		"channel_group_mobile_topic_id":     binding.TopicID,
		"channel_group_mobile_binding_user": binding.CanonicalUID,
		"weixin_clawbot_token_id":           token.ID,
		"weixin_clawbot_bot_user_id":        strings.TrimSpace(msg.ToUserID),
	}
}

type httpWeixinClawBotAPI struct {
	baseURL         string
	httpClient      *http.Client
	longPollTimeout time.Duration
}

func newHTTPWeixinClawBotAPI(cfg WeixinClawBotConfig) *httpWeixinClawBotAPI {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.ILinkBaseURL), "/")
	if baseURL == "" {
		baseURL = configuredWeixinClawBotILinkBaseURL()
	}
	longPoll := cfg.LongPollTimeout
	if longPoll <= 0 {
		longPoll = 30 * time.Second
	}
	return &httpWeixinClawBotAPI{
		baseURL:         baseURL,
		httpClient:      &http.Client{Timeout: longPoll + 5*time.Second},
		longPollTimeout: longPoll,
	}
}

func (c *httpWeixinClawBotAPI) GetQRCodeStatus(ctx context.Context, qrcode string) (*weixinClawBotQRCodeStatus, error) {
	endpoint := c.baseURL + "/ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(qrcode)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data weixinClawBotQRCodeStatus
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || data.Ret != 0 || data.ErrCode != 0 {
		return nil, &weixinClawBotAPIError{Operation: "qrcode-status", Status: resp.StatusCode, Ret: data.Ret, ErrCode: data.ErrCode, ErrMsg: data.ErrMsg}
	}
	return &data, nil
}

func (c *httpWeixinClawBotAPI) GetUpdates(ctx context.Context, botToken string, getUpdatesBuf string) (*weixinClawBotUpdates, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"get_updates_buf": strings.TrimSpace(getUpdatesBuf),
		"base_info": map[string]string{
			"channel_version": weixinClawBotChannelVersion,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/ilink/bot/getupdates", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setWeixinClawBotAuthHeaders(req, botToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data weixinClawBotUpdates
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || data.Ret != 0 || data.ErrCode != 0 {
		return nil, &weixinClawBotAPIError{Operation: "getupdates", Status: resp.StatusCode, Ret: data.Ret, ErrCode: data.ErrCode, ErrMsg: data.ErrMsg}
	}
	return &data, nil
}

func (c *httpWeixinClawBotAPI) SendTextMessage(ctx context.Context, botToken string, toUserID string, text string, contextToken string, fromUserID string) error {
	if strings.TrimSpace(contextToken) == "" {
		return errors.New("context_token is required for Weixin ClawBot sendmessage")
	}
	clientID := "catsco-" + randomClawBotHex(6)
	body, _ := json.Marshal(map[string]interface{}{
		"msg": map[string]interface{}{
			"from_user_id":  strings.TrimSpace(fromUserID),
			"to_user_id":    strings.TrimSpace(toUserID),
			"client_id":     clientID,
			"message_type":  2,
			"message_state": 2,
			"item_list": []map[string]interface{}{
				{"type": 1, "text_item": map[string]string{"text": text}},
			},
			"context_token": strings.TrimSpace(contextToken),
		},
		"base_info": map[string]string{
			"channel_version": weixinClawBotChannelVersion,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/ilink/bot/sendmessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	setWeixinClawBotAuthHeaders(req, botToken)
	req.Header.Set("X-WECHAT-UIN", randomWechatUIN())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var data struct {
		Ret     int    `json:"ret,omitempty"`
		ErrCode int    `json:"errcode,omitempty"`
		ErrMsg  string `json:"errmsg,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || data.Ret != 0 || data.ErrCode != 0 {
		return &weixinClawBotAPIError{Operation: "sendmessage", Status: resp.StatusCode, Ret: data.Ret, ErrCode: data.ErrCode, ErrMsg: data.ErrMsg}
	}
	return nil
}

func setWeixinClawBotAuthHeaders(req *http.Request, botToken string) {
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(botToken))
	req.Header.Set("AuthorizationType", "ilink_bot_token")
	req.Header.Set("Content-Type", "application/json")
}

func weixinClawBotMessageText(msg weixinClawBotMessage) string {
	var parts []string
	for _, item := range msg.ItemList {
		if item.Type == 1 && strings.TrimSpace(item.TextItem.Text) != "" {
			parts = append(parts, item.TextItem.Text)
		}
	}
	return strings.Join(parts, "")
}

func clawBotMessageID(msg weixinClawBotMessage) string {
	raw := strings.TrimSpace(string(msg.MessageID))
	raw = strings.Trim(raw, `"`)
	return raw
}

func weixinClawBotClientMsgID(token *types.WeixinClawBotToken, msg weixinClawBotMessage) string {
	messageID := clawBotMessageID(msg)
	if messageID == "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s", token.ID, msg.FromUserID, msg.ToUserID, weixinClawBotMessageText(msg))))
		messageID = hex.EncodeToString(sum[:16])
	}
	return "weixin_clawbot:" + token.TokenHash + ":" + messageID
}

func hashWeixinClawBotToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func last4(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4 {
		return value
	}
	return value[len(value)-4:]
}

func copyWeixinClawBotContexts(contexts map[string]types.WeixinClawBotContext) map[string]types.WeixinClawBotContext {
	next := make(map[string]types.WeixinClawBotContext, len(contexts))
	for key, value := range contexts {
		next[key] = value
	}
	return next
}

func randomClawBotHex(n int) string {
	if n <= 0 {
		n = 6
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func randomWechatUIN() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	n := binary.BigEndian.Uint32(buf)
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", n)))
}

func sleepWithContext(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func nextBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return time.Second
	}
	next := current * 2
	if next > 30*time.Second {
		return 30 * time.Second
	}
	return next
}

func secondsEnvDuration(name string, defaultSeconds int, minSeconds int, maxSeconds int) time.Duration {
	value := strings.TrimSpace(firstEnv(name))
	seconds := defaultSeconds
	if value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			seconds = parsed
		}
	}
	if seconds < minSeconds {
		seconds = minSeconds
	}
	if maxSeconds > 0 && seconds > maxSeconds {
		seconds = maxSeconds
	}
	return time.Duration(seconds) * time.Second
}

func falseEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off", "disabled":
		return true
	default:
		return false
	}
}
