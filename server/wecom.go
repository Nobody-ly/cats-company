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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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

	mu       sync.Mutex
	queues   map[int64][]WeComEvent
	notify   map[int64]chan struct{}
	seen     map[int64]map[string]time.Time
	tokenMap map[int64]weComAccessToken
}

type weComAccessToken struct {
	token     string
	expiresAt time.Time
}

type WeComEvent struct {
	ID         string `json:"id"`
	FromUser  string `json:"from_user"`
	ToUser    string `json:"to_user"`
	MsgType   string `json:"msg_type"`
	Content   string `json:"content"`
	AgentID   string `json:"agent_id,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type weComConfigRequest struct {
	CorpID         string `json:"corp_id"`
	AgentID        string `json:"agent_id"`
	Secret         string `json:"secret"`
	CallbackToken  string `json:"callback_token"`
	EncodingAESKey string `json:"encoding_aes_key"`
	APIBaseURL     string `json:"api_base_url"`
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
		db:     db,
		client: &http.Client{Timeout: 15 * time.Second},
		queues:   make(map[int64][]WeComEvent),
		notify:   make(map[int64]chan struct{}),
		seen:     make(map[int64]map[string]time.Time),
		tokenMap: make(map[int64]weComAccessToken),
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
		cfg, err := h.db.GetWeComConfig(botUID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load wecom config"})
			return
		}
		writeJSON(w, http.StatusOK, h.configResponse(r, botUID, cfg))
	case http.MethodPost:
		var req weComConfigRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		cfg, err := normalizeWeComConfig(botUID, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := h.db.SaveWeComConfig(botUID, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save wecom config"})
			return
		}
		writeJSON(w, http.StatusOK, h.configResponse(r, botUID, cfg))
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

	cfg, err := h.db.GetWeComConfig(botUID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load wecom config"})
		return
	}
	if cfg == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "wecom config not found"})
		return
	}
	if strings.TrimSpace(cfg.AgentID) == "" || strings.TrimSpace(cfg.Secret) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "wecom agent_id and secret required before sending messages"})
		return
	}

	if err := h.sendText(r.Context(), cfg, req.ToUser, req.Content); err != nil {
		log.Printf("[wecom] send failed bot=%d to=%s: %v", botUID, req.ToUser, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "wecom send failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *WeComHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	botUID, err := parseWeComCallbackBotUID(r.URL.Path)
	if err != nil {
		http.Error(w, "bad callback path", http.StatusBadRequest)
		return
	}

	cfg, err := h.db.GetWeComConfig(botUID)
	if err != nil || cfg == nil {
		http.Error(w, "wecom config not found", http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleVerifyCallback(w, r, cfg)
	case http.MethodPost:
		h.handleMessageCallback(w, r, botUID, cfg)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *WeComHandler) handleVerifyCallback(w http.ResponseWriter, r *http.Request, cfg *types.WeComConfig) {
	q := r.URL.Query()
	encryptedEcho := q.Get("echostr")
	if err := verifyWeComSignature(cfg.CallbackToken, q.Get("msg_signature"), q.Get("timestamp"), q.Get("nonce"), encryptedEcho); err != nil {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	plain, err := decryptWeComPayload(cfg.EncodingAESKey, encryptedEcho, cfg.CorpID)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(plain)
}

func (h *WeComHandler) handleMessageCallback(w http.ResponseWriter, r *http.Request, botUID int64, cfg *types.WeComConfig) {
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
	if err := verifyWeComSignature(cfg.CallbackToken, q.Get("msg_signature"), q.Get("timestamp"), q.Get("nonce"), envelope.Encrypt); err != nil {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	plainXML, err := decryptWeComPayload(cfg.EncodingAESKey, envelope.Encrypt, cfg.CorpID)
	if err != nil {
		http.Error(w, "decrypt failed", http.StatusBadRequest)
		return
	}

	var msg weComPlainMessage
	if err := xml.Unmarshal(plainXML, &msg); err != nil {
		http.Error(w, "invalid message xml", http.StatusBadRequest)
		return
	}

	event := weComEventFromMessage(msg)
	if event.ID == "" {
		event.ID = hex.EncodeToString(sha1Bytes(plainXML))
	}
	if !h.markSeen(botUID, event.ID) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
		return
	}
	h.enqueue(botUID, event)

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("success"))
}

func (h *WeComHandler) configResponse(r *http.Request, botUID int64, cfg *types.WeComConfig) map[string]interface{} {
	resp := map[string]interface{}{
		"configured":   cfg != nil,
		"callback_url": buildWeComCallbackURL(r, botUID),
	}
	if cfg != nil {
		resp["corp_id"] = cfg.CorpID
		resp["agent_id"] = cfg.AgentID
		resp["api_base_url"] = cfg.APIBaseURL
		resp["secret_present"] = cfg.Secret != ""
		resp["callback_token_present"] = cfg.CallbackToken != ""
		resp["encoding_aes_key_present"] = cfg.EncodingAESKey != ""
		resp["ready_for_callback"] = cfg.CorpID != "" && cfg.CallbackToken != "" && cfg.EncodingAESKey != ""
		resp["ready_for_send"] = cfg.CorpID != "" && cfg.AgentID != "" && cfg.Secret != "" && cfg.CallbackToken != "" && cfg.EncodingAESKey != ""
		resp["enabled"] = cfg.Enabled
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

func (h *WeComHandler) markSeen(botUID int64, id string) bool {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()

	seen := h.seen[botUID]
	if seen == nil {
		seen = make(map[string]time.Time)
		h.seen[botUID] = seen
	}
	for key, ts := range seen {
		if now.Sub(ts) > 10*time.Minute {
			delete(seen, key)
		}
	}
	if _, ok := seen[id]; ok {
		return false
	}
	seen[id] = now
	return true
}

func (h *WeComHandler) sendText(ctx context.Context, cfg *types.WeComConfig, toUser, content string) error {
	token, err := h.accessToken(ctx, cfg)
	if err != nil {
		return err
	}

	agentID, err := strconv.Atoi(strings.TrimSpace(cfg.AgentID))
	if err != nil {
		return fmt.Errorf("invalid wecom agent id: %w", err)
	}

	payload := map[string]interface{}{
		"touser":  toUser,
		"msgtype": "text",
		"agentid": agentID,
		"text": map[string]string{
			"content": content,
		},
	}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(cfg.APIBaseURL, "/") + "/cgi-bin/message/send?access_token=" + url.QueryEscape(token)
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

	var parsed struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode wecom send response: %w", err)
	}
	if resp.StatusCode >= 300 || parsed.ErrCode != 0 {
		return fmt.Errorf("wecom send errcode=%d errmsg=%s", parsed.ErrCode, parsed.ErrMsg)
	}
	return nil
}

func (h *WeComHandler) accessToken(ctx context.Context, cfg *types.WeComConfig) (string, error) {
	h.mu.Lock()
	cached := h.tokenMap[cfg.BotUID]
	if cached.token != "" && time.Now().Before(cached.expiresAt.Add(-2*time.Minute)) {
		h.mu.Unlock()
		return cached.token, nil
	}
	h.mu.Unlock()

	endpoint := fmt.Sprintf(
		"%s/cgi-bin/gettoken?corpid=%s&corpsecret=%s",
		strings.TrimRight(cfg.APIBaseURL, "/"),
		url.QueryEscape(cfg.CorpID),
		url.QueryEscape(cfg.Secret),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var parsed struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode wecom token response: %w", err)
	}
	if resp.StatusCode >= 300 || parsed.ErrCode != 0 || parsed.AccessToken == "" {
		return "", fmt.Errorf("wecom token errcode=%d errmsg=%s", parsed.ErrCode, parsed.ErrMsg)
	}
	if parsed.ExpiresIn <= 0 {
		parsed.ExpiresIn = 7200
	}

	h.mu.Lock()
	h.tokenMap[cfg.BotUID] = weComAccessToken{
		token:     parsed.AccessToken,
		expiresAt: time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second),
	}
	h.mu.Unlock()
	return parsed.AccessToken, nil
}

func normalizeWeComConfig(botUID int64, req weComConfigRequest) (*types.WeComConfig, error) {
	cfg := &types.WeComConfig{
		BotUID:         botUID,
		CorpID:         strings.TrimSpace(req.CorpID),
		AgentID:        strings.TrimSpace(req.AgentID),
		Secret:         strings.TrimSpace(req.Secret),
		CallbackToken:  strings.TrimSpace(req.CallbackToken),
		EncodingAESKey: strings.TrimSpace(req.EncodingAESKey),
		APIBaseURL:     strings.TrimRight(strings.TrimSpace(req.APIBaseURL), "/"),
		Enabled:        true,
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultWeComAPIBaseURL
	}

	switch {
	case cfg.CorpID == "":
		return nil, errors.New("corp_id required")
	case cfg.CallbackToken == "":
		return nil, errors.New("callback_token required")
	case cfg.EncodingAESKey == "":
		return nil, errors.New("encoding_aes_key required")
	case len(cfg.EncodingAESKey) != 43:
		return nil, errors.New("encoding_aes_key must be 43 chars")
	case !isHTTPURL(cfg.APIBaseURL):
		return nil, errors.New("api_base_url must be http(s) url")
	}
	return cfg, nil
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

func parseWeComCallbackBotUID(path string) (int64, error) {
	raw := strings.TrimPrefix(path, "/api/wecom/callback/")
	raw = strings.Trim(raw, "/")
	if raw == "" || strings.Contains(raw, "/") {
		return 0, fmt.Errorf("invalid callback path")
	}
	return strconv.ParseInt(raw, 10, 64)
}

func buildWeComCallbackURL(r *http.Request, botUID int64) string {
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
	return fmt.Sprintf("%s://%s/api/wecom/callback/%d", scheme, host, botUID)
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

func decryptWeComPayload(encodingAESKey, encrypted, corpID string) ([]byte, error) {
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
	if msgLen < 0 || end > len(plain) {
		return nil, fmt.Errorf("invalid message length")
	}

	if corpID != "" {
		actualCorpID := string(plain[end:])
		if actualCorpID != "" && actualCorpID != corpID {
			return nil, fmt.Errorf("corp id mismatch")
		}
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

func weComEventFromMessage(msg weComPlainMessage) WeComEvent {
	content := strings.TrimSpace(msg.Content)
	if content == "" && msg.MsgType != "text" {
		content = fmt.Sprintf("[Enterprise WeChat %s message is not supported yet]", msg.MsgType)
	}
	return WeComEvent{
		ID:         strings.TrimSpace(msg.MsgID),
		FromUser:   strings.TrimSpace(msg.FromUserName),
		ToUser:     strings.TrimSpace(msg.ToUserName),
		MsgType:    strings.TrimSpace(msg.MsgType),
		Content:    content,
		AgentID:    strings.TrimSpace(msg.AgentID),
		CreatedAt: msg.CreateTime,
	}
}

func sha1Bytes(data []byte) []byte {
	sum := sha1.Sum(data)
	return sum[:]
}

func isHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
