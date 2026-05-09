package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openchat/openchat/server/db/mysql"
	"github.com/openchat/openchat/server/store/types"
)

const defaultWeComAPIBaseURL = "https://qyapi.weixin.qq.com"

type WeComHandler struct {
	db     *mysql.Adapter
	client *http.Client
	suite  weComSuiteConfig

	mu          sync.Mutex
	queues      map[int64][]WeComEvent
	notify      map[int64]chan struct{}
	seen        map[string]time.Time
	corpTokens  map[string]weComAccessToken
	suiteTokens map[string]weComAccessToken
}

type weComSuiteConfig struct {
	SuiteID        string
	SuiteSecret    string
	Token          string
	EncodingAESKey string
	APIBaseURL     string
	PublicBaseURL  string
}

type weComAccessToken struct {
	token     string
	expiresAt time.Time
}

type WeComEvent struct {
	ID         string `json:"id"`
	AuthCorpID string `json:"auth_corp_id,omitempty"`
	FromUser   string `json:"from_user"`
	ToUser     string `json:"to_user"`
	MsgType    string `json:"msg_type"`
	Content    string `json:"content"`
	AgentID    string `json:"agent_id,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

type weComConfigRequest struct {
	AuthCorpID string `json:"auth_corp_id"`
	AgentID    string `json:"agent_id"`
}

type weComSendRequest struct {
	ToUser  string `json:"to_user"`
	Content string `json:"content"`
}

type weComEncryptedEnvelope struct {
	XMLName xml.Name `xml:"xml"`
	Encrypt string   `xml:"Encrypt"`
}

type weComPlainMessage struct {
	XMLName      xml.Name `xml:"xml"`
	SuiteID      string   `xml:"SuiteId"`
	InfoType     string   `xml:"InfoType"`
	SuiteTicket  string   `xml:"SuiteTicket"`
	AuthCode     string   `xml:"AuthCode"`
	AuthCorpID   string   `xml:"AuthCorpId"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        string   `xml:"MsgId"`
	AgentID      string   `xml:"AgentID"`
	Event        string   `xml:"Event"`
}

func NewWeComHandler(db *mysql.Adapter) *WeComHandler {
	return &WeComHandler{
		db:          db,
		client:      &http.Client{Timeout: 15 * time.Second},
		suite:       loadWeComSuiteConfigFromEnv(),
		queues:      make(map[int64][]WeComEvent),
		notify:      make(map[int64]chan struct{}),
		seen:        make(map[string]time.Time),
		corpTokens:  make(map[string]weComAccessToken),
		suiteTokens: make(map[string]weComAccessToken),
	}
}

func loadWeComSuiteConfigFromEnv() weComSuiteConfig {
	return weComSuiteConfig{
		SuiteID:        strings.TrimSpace(os.Getenv("WECOM_SUITE_ID")),
		SuiteSecret:    strings.TrimSpace(os.Getenv("WECOM_SUITE_SECRET")),
		Token:          strings.TrimSpace(os.Getenv("WECOM_SUITE_TOKEN")),
		EncodingAESKey: strings.TrimSpace(os.Getenv("WECOM_SUITE_ENCODING_AES_KEY")),
		APIBaseURL:     strings.TrimRight(firstNonEmptyWeCom(os.Getenv("WECOM_API_BASE_URL"), defaultWeComAPIBaseURL), "/"),
		PublicBaseURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("WECOM_PUBLIC_BASE_URL")), "/"),
	}
}

func (h *WeComHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	botUID := UIDFromContext(r.Context())
	if botUID == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		auth, err := h.db.GetWeComSuiteAuthByBotUID(botUID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load wecom binding"})
			return
		}
		writeJSON(w, http.StatusOK, h.configResponse(r, auth))
	case http.MethodPost:
		var req weComConfigRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		req.AuthCorpID = strings.TrimSpace(req.AuthCorpID)
		req.AgentID = strings.TrimSpace(req.AgentID)
		if req.AuthCorpID == "" || req.AgentID == "" {
			writeJSON(w, http.StatusOK, h.configResponse(r, nil))
			return
		}
		if err := h.db.BindWeComSuiteAuth(botUID, req.AuthCorpID, req.AgentID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "wecom authorization not found; install the third-party app first"})
			return
		}
		auth, _ := h.db.GetWeComSuiteAuthByBotUID(botUID)
		writeJSON(w, http.StatusOK, h.configResponse(r, auth))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *WeComHandler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	botUID := UIDFromContext(r.Context())
	if botUID == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	timeout := parseLongPollTimeout(r.URL.Query().Get("timeout"))
	events := h.dequeue(botUID)
	if len(events) == 0 && timeout > 0 {
		ch := h.notifyChan(botUID)
		select {
		case <-ch:
			events = h.dequeue(botUID)
		case <-time.After(timeout):
		case <-r.Context().Done():
			return
		}
	}

	if events == nil {
		events = []WeComEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": events})
}

func (h *WeComHandler) HandleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	botUID := UIDFromContext(r.Context())
	if botUID == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req weComSendRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	req.ToUser = strings.TrimSpace(req.ToUser)
	req.Content = strings.TrimSpace(req.Content)
	if req.ToUser == "" || req.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to_user and content required"})
		return
	}

	auth, err := h.db.GetWeComSuiteAuthByBotUID(botUID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load wecom authorization"})
		return
	}
	if auth == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "wecom authorization not bound"})
		return
	}

	if err := h.sendText(r.Context(), auth, req.ToUser, req.Content); err != nil {
		log.Printf("[wecom] send failed bot=%d corp=%s to=%s: %v", botUID, auth.AuthCorpID, req.ToUser, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "wecom send failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *WeComHandler) HandleSuiteDataCallback(w http.ResponseWriter, r *http.Request) {
	h.handleSuiteCallback(w, r, parseWeComSuiteCallbackCorpID(r.URL.Path))
}

func (h *WeComHandler) HandleSuiteCommandCallback(w http.ResponseWriter, r *http.Request) {
	h.handleSuiteCallback(w, r, "")
}

func (h *WeComHandler) handleSuiteCallback(w http.ResponseWriter, r *http.Request, pathCorpID string) {
	if !h.suite.isConfigured() {
		http.Error(w, "wecom suite is not configured", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleVerifyCallback(w, r)
	case http.MethodPost:
		h.handleMessageCallback(w, r, pathCorpID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *WeComHandler) handleVerifyCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	encryptedEcho := q.Get("echostr")
	if err := verifyWeComSignature(h.suite.Token, q.Get("msg_signature"), q.Get("timestamp"), q.Get("nonce"), encryptedEcho); err != nil {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	plain, err := decryptWeComPayload(h.suite.EncodingAESKey, encryptedEcho, h.suite.SuiteID)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(plain)
}

func (h *WeComHandler) handleMessageCallback(w http.ResponseWriter, r *http.Request, pathCorpID string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}

	var envelope weComEncryptedEnvelope
	if err := xml.Unmarshal(body, &envelope); err != nil || strings.TrimSpace(envelope.Encrypt) == "" {
		http.Error(w, "invalid encrypted xml", http.StatusBadRequest)
		return
	}

	q := r.URL.Query()
	if err := verifyWeComSignature(h.suite.Token, q.Get("msg_signature"), q.Get("timestamp"), q.Get("nonce"), envelope.Encrypt); err != nil {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	plainXML, err := decryptWeComPayload(h.suite.EncodingAESKey, envelope.Encrypt, h.suite.SuiteID)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}

	var msg weComPlainMessage
	if err := xml.Unmarshal(plainXML, &msg); err != nil {
		http.Error(w, "invalid message xml", http.StatusBadRequest)
		return
	}

	if err := h.dispatchSuiteMessage(r.Context(), msg, pathCorpID, plainXML); err != nil {
		log.Printf("[wecom] callback dispatch failed: %v", err)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("success"))
}

func (h *WeComHandler) dispatchSuiteMessage(ctx context.Context, msg weComPlainMessage, pathCorpID string, raw []byte) error {
	infoType := strings.TrimSpace(msg.InfoType)
	switch infoType {
	case "suite_ticket":
		if strings.TrimSpace(msg.SuiteTicket) == "" {
			return nil
		}
		return h.db.SaveWeComSuiteTicket(h.suite.SuiteID, strings.TrimSpace(msg.SuiteTicket))
	case "create_auth":
		if strings.TrimSpace(msg.AuthCode) == "" {
			return nil
		}
		return h.exchangeAndSavePermanentCode(ctx, strings.TrimSpace(msg.AuthCode), 0)
	case "change_auth":
		if strings.TrimSpace(msg.AuthCode) == "" {
			return nil
		}
		return h.exchangeAndSavePermanentCode(ctx, strings.TrimSpace(msg.AuthCode), 0)
	}

	event := weComEventFromMessage(msg, pathCorpID)
	if event.ID == "" {
		event.ID = hex.EncodeToString(sha1Bytes(raw))
	}
	if !h.markSeen(event.AuthCorpID, event.AgentID, event.ID) {
		return nil
	}

	auth, err := h.db.GetWeComSuiteAuth(event.AuthCorpID, event.AgentID)
	if err != nil || auth == nil || auth.BotUID == 0 {
		log.Printf("[wecom] unbound message ignored corp=%s agent=%s", event.AuthCorpID, event.AgentID)
		return err
	}
	h.enqueue(auth.BotUID, event)
	return nil
}

func (h *WeComHandler) configResponse(r *http.Request, auth *types.WeComSuiteAuth) map[string]interface{} {
	resp := map[string]interface{}{
		"mode":                     "suite",
		"suite_configured":         h.suite.isConfigured(),
		"suite_id_present":         h.suite.SuiteID != "",
		"suite_secret_present":     h.suite.SuiteSecret != "",
		"token_present":            h.suite.Token != "",
		"encoding_aes_key_present": h.suite.EncodingAESKey != "",
		"data_callback_url":        h.suite.callbackURL(r, "/api/wecom/suite/data"),
		"command_callback_url":     h.suite.callbackURL(r, "/api/wecom/suite/command"),
		"api_base_url":             h.suite.APIBaseURL,
		"bound":                    auth != nil,
	}
	if auth != nil {
		resp["auth_corp_id"] = auth.AuthCorpID
		resp["auth_corp_name"] = auth.AuthCorpName
		resp["agent_id"] = auth.AgentID
	}
	return resp
}

func (h *WeComHandler) enqueue(botUID int64, event WeComEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	queue := append(h.queues[botUID], event)
	if len(queue) > 100 {
		queue = queue[len(queue)-100:]
	}
	h.queues[botUID] = queue

	ch := h.getNotifyChanLocked(botUID)
	close(ch)
	h.notify[botUID] = make(chan struct{})
}

func (h *WeComHandler) dequeue(botUID int64) []WeComEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	events := h.queues[botUID]
	delete(h.queues, botUID)
	return events
}

func (h *WeComHandler) notifyChan(botUID int64) <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.getNotifyChanLocked(botUID)
}

func (h *WeComHandler) getNotifyChanLocked(botUID int64) chan struct{} {
	ch, ok := h.notify[botUID]
	if !ok {
		ch = make(chan struct{})
		h.notify[botUID] = ch
	}
	return ch
}

func (h *WeComHandler) markSeen(corpID, agentID, id string) bool {
	now := time.Now()
	key := corpID + ":" + agentID + ":" + id
	h.mu.Lock()
	defer h.mu.Unlock()
	for seenKey, ts := range h.seen {
		if now.Sub(ts) > 10*time.Minute {
			delete(h.seen, seenKey)
		}
	}
	if _, ok := h.seen[key]; ok {
		return false
	}
	h.seen[key] = now
	return true
}

func (h *WeComHandler) exchangeAndSavePermanentCode(ctx context.Context, authCode string, botUID int64) error {
	suiteToken, err := h.suiteAccessToken(ctx)
	if err != nil {
		return err
	}
	payload := map[string]string{"auth_code": authCode}
	var parsed struct {
		ErrCode       int    `json:"errcode"`
		ErrMsg        string `json:"errmsg"`
		PermanentCode string `json:"permanent_code"`
		AuthCorpInfo  struct {
			CorpID   string `json:"corpid"`
			CorpName string `json:"corp_name"`
		} `json:"auth_corp_info"`
		AuthInfo struct {
			Agent []struct {
				AgentID int `json:"agentid"`
			} `json:"agent"`
		} `json:"auth_info"`
	}
	if err := h.postWeComJSON(ctx, "/cgi-bin/service/get_permanent_code?suite_access_token="+url.QueryEscape(suiteToken), payload, &parsed); err != nil {
		return err
	}
	if parsed.ErrCode != 0 || parsed.PermanentCode == "" || parsed.AuthCorpInfo.CorpID == "" {
		return fmt.Errorf("wecom permanent_code errcode=%d errmsg=%s", parsed.ErrCode, parsed.ErrMsg)
	}
	agentID := ""
	if len(parsed.AuthInfo.Agent) > 0 {
		agentID = strconv.Itoa(parsed.AuthInfo.Agent[0].AgentID)
	}
	if agentID == "" {
		return fmt.Errorf("wecom auth has no agent id")
	}
	return h.db.SaveWeComSuiteAuth(&types.WeComSuiteAuth{
		AuthCorpID:    parsed.AuthCorpInfo.CorpID,
		AuthCorpName:  parsed.AuthCorpInfo.CorpName,
		AgentID:       agentID,
		PermanentCode: parsed.PermanentCode,
		BotUID:        botUID,
		Enabled:       true,
	})
}

func (h *WeComHandler) sendText(ctx context.Context, auth *types.WeComSuiteAuth, toUser, content string) error {
	token, err := h.corpAccessToken(ctx, auth)
	if err != nil {
		return err
	}

	agentID, err := strconv.Atoi(strings.TrimSpace(auth.AgentID))
	if err != nil {
		return fmt.Errorf("invalid wecom agent id: %w", err)
	}

	payload := map[string]interface{}{
		"touser":  toUser,
		"msgtype": "text",
		"agentid": agentID,
		"text":    map[string]string{"content": content},
	}
	var parsed struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := h.postWeComJSON(ctx, "/cgi-bin/message/send?access_token="+url.QueryEscape(token), payload, &parsed); err != nil {
		return err
	}
	if parsed.ErrCode != 0 {
		return fmt.Errorf("wecom send errcode=%d errmsg=%s", parsed.ErrCode, parsed.ErrMsg)
	}
	return nil
}

func (h *WeComHandler) suiteAccessToken(ctx context.Context) (string, error) {
	h.mu.Lock()
	cached := h.suiteTokens[h.suite.SuiteID]
	if cached.token != "" && time.Now().Before(cached.expiresAt.Add(-2*time.Minute)) {
		h.mu.Unlock()
		return cached.token, nil
	}
	h.mu.Unlock()

	state, err := h.db.GetWeComSuiteState(h.suite.SuiteID)
	if err != nil {
		return "", err
	}
	if state != nil && state.SuiteAccessToken != "" && state.SuiteAccessTokenExpiresAt != nil && time.Now().Before(state.SuiteAccessTokenExpiresAt.Add(-2*time.Minute)) {
		return state.SuiteAccessToken, nil
	}
	if state == nil || strings.TrimSpace(state.SuiteTicket) == "" {
		return "", fmt.Errorf("wecom suite_ticket is not ready yet")
	}

	payload := map[string]string{
		"suite_id":     h.suite.SuiteID,
		"suite_secret": h.suite.SuiteSecret,
		"suite_ticket": state.SuiteTicket,
	}
	var parsed struct {
		ErrCode          int    `json:"errcode"`
		ErrMsg           string `json:"errmsg"`
		SuiteAccessToken string `json:"suite_access_token"`
		ExpiresIn        int    `json:"expires_in"`
	}
	if err := h.postWeComJSON(ctx, "/cgi-bin/service/get_suite_token", payload, &parsed); err != nil {
		return "", err
	}
	if parsed.ErrCode != 0 || parsed.SuiteAccessToken == "" {
		return "", fmt.Errorf("wecom suite token errcode=%d errmsg=%s", parsed.ErrCode, parsed.ErrMsg)
	}
	if parsed.ExpiresIn <= 0 {
		parsed.ExpiresIn = 7200
	}
	expiresAt := time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	if err := h.db.SaveWeComSuiteToken(h.suite.SuiteID, parsed.SuiteAccessToken, expiresAt); err != nil {
		return "", err
	}
	h.mu.Lock()
	h.suiteTokens[h.suite.SuiteID] = weComAccessToken{token: parsed.SuiteAccessToken, expiresAt: expiresAt}
	h.mu.Unlock()
	return parsed.SuiteAccessToken, nil
}

func (h *WeComHandler) corpAccessToken(ctx context.Context, auth *types.WeComSuiteAuth) (string, error) {
	cacheKey := auth.AuthCorpID + ":" + auth.AgentID
	h.mu.Lock()
	cached := h.corpTokens[cacheKey]
	if cached.token != "" && time.Now().Before(cached.expiresAt.Add(-2*time.Minute)) {
		h.mu.Unlock()
		return cached.token, nil
	}
	h.mu.Unlock()

	suiteToken, err := h.suiteAccessToken(ctx)
	if err != nil {
		return "", err
	}
	payload := map[string]string{
		"auth_corpid":    auth.AuthCorpID,
		"permanent_code": auth.PermanentCode,
	}
	var parsed struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := h.postWeComJSON(ctx, "/cgi-bin/service/get_corp_token?suite_access_token="+url.QueryEscape(suiteToken), payload, &parsed); err != nil {
		return "", err
	}
	if parsed.ErrCode != 0 || parsed.AccessToken == "" {
		return "", fmt.Errorf("wecom corp token errcode=%d errmsg=%s", parsed.ErrCode, parsed.ErrMsg)
	}
	if parsed.ExpiresIn <= 0 {
		parsed.ExpiresIn = 7200
	}
	token := weComAccessToken{token: parsed.AccessToken, expiresAt: time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second)}
	h.mu.Lock()
	h.corpTokens[cacheKey] = token
	h.mu.Unlock()
	return token.token, nil
}

func (h *WeComHandler) postWeComJSON(ctx context.Context, path string, payload interface{}, out interface{}) error {
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(h.suite.APIBaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode wecom response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("wecom http status: %d", resp.StatusCode)
	}
	return nil
}

func parseLongPollTimeout(raw string) time.Duration {
	seconds := 30
	if raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			seconds = parsed
		}
	}
	if seconds < 0 {
		seconds = 0
	}
	if seconds > 60 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

func parseWeComSuiteCallbackCorpID(path string) string {
	raw := strings.TrimPrefix(path, "/api/wecom/suite/data")
	raw = strings.Trim(raw, "/")
	if raw == "" || strings.Contains(raw, "/") {
		return ""
	}
	return raw
}

func (c weComSuiteConfig) callbackURL(r *http.Request, path string) string {
	if c.PublicBaseURL != "" {
		return c.PublicBaseURL + path
	}
	return buildWeComSuiteCallbackURL(r, path)
}

func buildWeComSuiteCallbackURL(r *http.Request, path string) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return fmt.Sprintf("%s://%s%s", scheme, host, path)
}

func verifyWeComSignature(token, signature, timestamp, nonce, encrypted string) error {
	parts := []string{token, timestamp, nonce, encrypted}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	expected := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(expected), []byte(strings.TrimSpace(signature))) != 1 {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

func decryptWeComPayload(encodingAESKey, encrypted, receiveID string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, fmt.Errorf("decode aes key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid aes key length")
	}

	cipherText, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, fmt.Errorf("decode encrypted payload: %w", err)
	}
	if len(cipherText) == 0 || len(cipherText)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("invalid encrypted payload length")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, key[:aes.BlockSize]).CryptBlocks(plain, cipherText)

	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	if len(plain) < 20 {
		return nil, fmt.Errorf("payload too short")
	}

	msgLen := int(binary.BigEndian.Uint32(plain[16:20]))
	start := 20
	end := start + msgLen
	if end > len(plain) {
		return nil, fmt.Errorf("invalid message length")
	}

	actualReceiveID := string(plain[end:])
	if receiveID != "" && actualReceiveID != "" && actualReceiveID != receiveID {
		return nil, fmt.Errorf("receive id mismatch")
	}
	return plain[start:end], nil
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("invalid pkcs7 data")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, fmt.Errorf("invalid pkcs7 padding")
	}
	for _, value := range data[len(data)-padding:] {
		if int(value) != padding {
			return nil, fmt.Errorf("invalid pkcs7 padding")
		}
	}
	return data[:len(data)-padding], nil
}

func weComEventFromMessage(msg weComPlainMessage, pathCorpID string) WeComEvent {
	authCorpID := firstNonEmptyWeCom(strings.TrimSpace(msg.AuthCorpID), strings.TrimSpace(pathCorpID))
	content := strings.TrimSpace(msg.Content)
	if content == "" && msg.MsgType != "text" {
		content = fmt.Sprintf("[Enterprise WeChat %s message is not supported yet]", msg.MsgType)
	}
	return WeComEvent{
		ID:         strings.TrimSpace(msg.MsgID),
		AuthCorpID: authCorpID,
		FromUser:   strings.TrimSpace(msg.FromUserName),
		ToUser:     strings.TrimSpace(msg.ToUserName),
		MsgType:    strings.TrimSpace(msg.MsgType),
		Content:    content,
		AgentID:    strings.TrimSpace(msg.AgentID),
		CreatedAt:  msg.CreateTime,
	}
}

func sha1Bytes(data []byte) []byte {
	sum := sha1.Sum(data)
	return sum[:]
}

func (c weComSuiteConfig) isConfigured() bool {
	return c.SuiteID != "" && c.SuiteSecret != "" && c.Token != "" && c.EncodingAESKey != "" && c.APIBaseURL != ""
}

func firstNonEmptyWeCom(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
