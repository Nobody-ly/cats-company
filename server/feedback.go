// Package server implements user feedback reporting.
package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openchat/openchat/server/store"
	"github.com/openchat/openchat/server/store/types"
)

const (
	maxFeedbackDescriptionLength = 5000
	maxFeedbackTitleLength       = 160
	maxFeedbackPageURLLength     = 1024
	maxFeedbackUserAgentLength   = 512
	maxFeedbackAttachments       = 5
)

// FeedbackHandler handles user feedback reports.
type FeedbackHandler struct {
	db store.Store
}

// NewFeedbackHandler creates a new FeedbackHandler.
func NewFeedbackHandler(db store.Store) *FeedbackHandler {
	return &FeedbackHandler{db: db}
}

// FeedbackRequest is the JSON body for POST /api/feedback.
type FeedbackRequest struct {
	Category    string                     `json:"category"`
	Title       string                     `json:"title,omitempty"`
	Description string                     `json:"description"`
	PageURL     string                     `json:"page_url,omitempty"`
	UserAgent   string                     `json:"user_agent,omitempty"`
	Attachments []types.FeedbackAttachment `json:"attachments,omitempty"`
}

// HandleCreateFeedback handles POST /api/feedback.
func (h *FeedbackHandler) HandleCreateFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	uid := UIDFromContext(r.Context())
	if uid == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var req FeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	report, err := buildFeedbackReport(uid, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	id, err := h.db.CreateFeedbackReport(report)
	if err != nil {
		fmt.Printf("[FEEDBACK_ERROR] failed to save feedback from user %d: %v\n", uid, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to submit feedback"})
		return
	}
	report.ID = id

	user, _ := h.db.GetUser(uid)
	go notifyFeedbackReport(report, user)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"id":      id,
	})
}

func buildFeedbackReport(uid int64, req FeedbackRequest) (*types.FeedbackReport, error) {
	category := strings.ToLower(strings.TrimSpace(req.Category))
	if category == "" {
		category = "bug"
	}
	switch category {
	case "bug", "suggestion", "other":
	default:
		return nil, fmt.Errorf("invalid category")
	}

	title := truncateString(strings.TrimSpace(req.Title), maxFeedbackTitleLength)
	description := strings.TrimSpace(req.Description)
	if description == "" {
		return nil, fmt.Errorf("description required")
	}
	if len([]rune(description)) > maxFeedbackDescriptionLength {
		return nil, fmt.Errorf("description too long")
	}
	if len(req.Attachments) > maxFeedbackAttachments {
		return nil, fmt.Errorf("too many attachments")
	}

	attachments := make([]types.FeedbackAttachment, 0, len(req.Attachments))
	for _, attachment := range req.Attachments {
		fileKey := strings.TrimSpace(attachment.FileKey)
		url := strings.TrimSpace(attachment.URL)
		if fileKey == "" || url == "" {
			return nil, fmt.Errorf("invalid attachment")
		}
		attachments = append(attachments, types.FeedbackAttachment{
			FileKey: fileKey,
			URL:     truncateString(url, 512),
			Name:    truncateString(strings.TrimSpace(attachment.Name), 255),
			Size:    attachment.Size,
			Type:    truncateString(strings.TrimSpace(attachment.Type), 32),
		})
	}

	return &types.FeedbackReport{
		UserID:      uid,
		Category:    category,
		Title:       title,
		Description: description,
		PageURL:     truncateString(strings.TrimSpace(req.PageURL), maxFeedbackPageURLLength),
		UserAgent:   truncateString(strings.TrimSpace(req.UserAgent), maxFeedbackUserAgentLength),
		Status:      "open",
		Attachments: attachments,
	}, nil
}

func truncateString(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

func notifyFeedbackReport(report *types.FeedbackReport, user *types.User) {
	webhookURL := strings.TrimSpace(os.Getenv("FEEDBACK_FEISHU_WEBHOOK_URL"))
	if webhookURL == "" {
		webhookURL = strings.TrimSpace(os.Getenv("FEISHU_WEBHOOK_URL"))
	}
	if webhookURL == "" || report == nil {
		return
	}

	text := buildFeedbackNotificationText(report, user)
	payload := map[string]interface{}{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		fmt.Printf("[FEEDBACK_NOTIFY_ERROR] marshal failed: %v\n", err)
		return
	}

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("[FEEDBACK_NOTIFY_ERROR] send failed: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Printf("[FEEDBACK_NOTIFY_ERROR] unexpected status: %s\n", resp.Status)
	}
}

func buildFeedbackNotificationText(report *types.FeedbackReport, user *types.User) string {
	keyword := strings.TrimSpace(os.Getenv("FEEDBACK_FEISHU_KEYWORD"))
	if keyword == "" {
		keyword = strings.TrimSpace(os.Getenv("FEISHU_KEYWORD"))
	}
	if keyword == "" {
		keyword = "cats-company"
	}

	userLabel := fmt.Sprintf("uid=%d", report.UserID)
	if user != nil {
		userLabel = fmt.Sprintf("%s (%s, uid=%d)", user.DisplayName, user.Username, user.ID)
	}

	var builder strings.Builder
	builder.WriteString("[")
	builder.WriteString(keyword)
	builder.WriteString("] 新反馈 #")
	builder.WriteString(fmt.Sprintf("%d", report.ID))
	builder.WriteString("\n用户: ")
	builder.WriteString(userLabel)
	builder.WriteString("\n类型: ")
	builder.WriteString(report.Category)
	if report.Title != "" {
		builder.WriteString("\n标题: ")
		builder.WriteString(report.Title)
	}
	if report.PageURL != "" {
		builder.WriteString("\n页面: ")
		builder.WriteString(report.PageURL)
	}
	builder.WriteString("\n描述:\n")
	builder.WriteString(report.Description)
	if len(report.Attachments) > 0 {
		builder.WriteString("\n截图（点击可直接打开）:")
		for _, attachment := range report.Attachments {
			builder.WriteString("\n- ")
			if attachment.Name != "" {
				builder.WriteString(attachment.Name)
				builder.WriteString(": ")
			}
			builder.WriteString(absoluteFeedbackURL(attachment.URL, report.PageURL))
		}
	}
	return builder.String()
}

func absoluteFeedbackURL(rawURL, pageURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		return rawURL
	}

	baseURL := strings.TrimSpace(os.Getenv("FEEDBACK_PUBLIC_BASE_URL"))
	if baseURL == "" {
		baseURL = originFromURL(pageURL)
	}
	if baseURL == "" {
		return rawURL
	}

	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasPrefix(rawURL, "/") {
		return baseURL + rawURL
	}
	return baseURL + "/" + rawURL
}

func originFromURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}
