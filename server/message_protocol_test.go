package server

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/openchat/openchat/server/store/types"
)

func TestPubMessageNormalizesLikeHTTPRequest(t *testing.T) {
	cases := []struct {
		name     string
		content  json.RawMessage
		msgType  string
		metadata map[string]interface{}
	}{
		{
			name:    "tool use",
			content: json.RawMessage(`"glob"`),
			msgType: "tool_use",
			metadata: map[string]interface{}{
				"id": "call_1",
				"input": map[string]interface{}{
					"pattern": "**/*.md",
				},
			},
		},
		{
			name:    "image content",
			content: json.RawMessage(`{"type":"image","payload":{"url":"/uploads/a.png","name":"a.png","size":12}}`),
			msgType: "image",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			httpPayload, err := normalizeMessageRequest(&SendMessageRequest{
				TopicID:  "grp_80",
				Type:     tc.msgType,
				Content:  tc.content,
				Metadata: tc.metadata,
			})
			if err != nil {
				t.Fatalf("normalize HTTP request: %v", err)
			}

			wsReq := messageRequestFromPub(&MsgClientPub{
				Topic:    "grp_80",
				Type:     tc.msgType,
				Content:  tc.content,
				Metadata: tc.metadata,
			})
			wsPayload, err := normalizeMessageRequest(wsReq)
			if err != nil {
				t.Fatalf("normalize WebSocket pub: %v", err)
			}

			if !reflect.DeepEqual(httpPayload, wsPayload) {
				t.Fatalf("payload mismatch\nHTTP: %#v\nWS:   %#v", httpPayload, wsPayload)
			}
		})
	}
}

func TestRuntimePlanMessageIsTransient(t *testing.T) {
	payload, err := normalizeMessageRequest(&SendMessageRequest{
		TopicID: "p2p_1_2",
		Type:    "runtime_plan",
		Content: json.RawMessage(`{"revision":1,"steps":[{"text":"检查链路","status":"in_progress"}]}`),
		Metadata: map[string]interface{}{
			"transient": true,
		},
	})
	if err != nil {
		t.Fatalf("normalize runtime plan: %v", err)
	}

	if !isTransientRuntimePayload(payload) {
		t.Fatalf("runtime_plan with transient metadata should not be stored")
	}
	if payload.DisplayType != "runtime_plan" {
		t.Fatalf("DisplayType = %q, want runtime_plan", payload.DisplayType)
	}
}

func TestContentBlocksKeepAttachmentPayload(t *testing.T) {
	payload, err := normalizeMessageRequest(&SendMessageRequest{
		TopicID: "grp_80",
		Type:    "text",
		Content: json.RawMessage(`"帮我看这张图"`),
		ContentBlocks: []types.ContentBlock{
			{Type: "text", Text: "帮我看这张图"},
			{
				Type: "image",
				Payload: map[string]interface{}{
					"file_key": "images/a.png",
					"url":      "/uploads/images/a.png",
					"name":     "a.png",
					"size":     float64(12),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("normalize request: %v", err)
	}

	if len(payload.ContentBlocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(payload.ContentBlocks))
	}
	if got := payload.ContentBlocks[1].Payload["url"]; got != "/uploads/images/a.png" {
		t.Fatalf("attachment payload url was not preserved: %#v", got)
	}
}

func TestClientMessageIDNormalizesFromTopLevelAndMetadata(t *testing.T) {
	payload, err := normalizeMessageRequest(&SendMessageRequest{
		TopicID:     "p2p_1_2",
		ClientMsgID: " catsco-explicit ",
		Content:     json.RawMessage(`"hello"`),
		Metadata: map[string]interface{}{
			"client_msg_id": "catsco-metadata",
		},
	})
	if err != nil {
		t.Fatalf("normalize explicit client_msg_id: %v", err)
	}
	if payload.ClientMsgID != "catsco-explicit" {
		t.Fatalf("ClientMsgID = %q, want explicit value", payload.ClientMsgID)
	}

	payload, err = normalizeMessageRequest(&SendMessageRequest{
		TopicID: "p2p_1_2",
		Content: json.RawMessage(`"hello"`),
		Metadata: map[string]interface{}{
			"client_msg_id": " catsco-metadata ",
		},
	})
	if err != nil {
		t.Fatalf("normalize metadata client_msg_id: %v", err)
	}
	if payload.ClientMsgID != "catsco-metadata" {
		t.Fatalf("ClientMsgID = %q, want metadata value", payload.ClientMsgID)
	}
}

func TestSaveNormalizedMessageUsesIdempotentStore(t *testing.T) {
	store := &idempotentMessageStore{id: 42, duplicate: true}
	payload, err := normalizeMessageRequest(&SendMessageRequest{
		TopicID:     "p2p_1_2",
		ClientMsgID: "catsco-1",
		Content:     json.RawMessage(`"hello"`),
	})
	if err != nil {
		t.Fatalf("normalize request: %v", err)
	}

	result, err := saveNormalizedMessage(store, "p2p_1_2", 1, 0, payload)
	if err != nil {
		t.Fatalf("save normalized message: %v", err)
	}
	if result.ID != 42 || !result.Duplicate {
		t.Fatalf("result = %#v, want id=42 duplicate=true", result)
	}
	if store.clientMsgID != "catsco-1" || store.calls != 1 {
		t.Fatalf("idempotent save was not used: store=%#v", store)
	}
}

func TestExtractPeerUIDRequiresSenderInTopic(t *testing.T) {
	if got := extractPeerUID("p2p_1_2", 1); got != 2 {
		t.Fatalf("extractPeerUID for uid 1 = %d, want 2", got)
	}
	if got := extractPeerUID("p2p_1_2", 2); got != 1 {
		t.Fatalf("extractPeerUID for uid 2 = %d, want 1", got)
	}
	if got := extractPeerUID("p2p_1_2", 3); got != 0 {
		t.Fatalf("extractPeerUID for non-member uid = %d, want 0", got)
	}
}

type idempotentMessageStore struct {
	id          int64
	duplicate   bool
	calls       int
	clientMsgID string
}

func (s *idempotentMessageStore) CreateTopic(id, topicType string, ownerID int64) error { return nil }
func (s *idempotentMessageStore) SaveMessage(topicID string, fromUID int64, content, msgType string) (int64, error) {
	return 0, errors.New("legacy SaveMessage should not be called")
}
func (s *idempotentMessageStore) SaveMessageWithBlocks(topicID string, fromUID int64, content string, blocks []types.ContentBlock, mode, role, msgType string) (int64, error) {
	return 0, errors.New("legacy SaveMessageWithBlocks should not be called")
}
func (s *idempotentMessageStore) SaveMessageWithReply(topicID string, fromUID int64, content, msgType string, replyTo int64) (int64, error) {
	return 0, errors.New("legacy SaveMessageWithReply should not be called")
}
func (s *idempotentMessageStore) SaveMessageIdempotent(topicID string, fromUID int64, content string, blocks []types.ContentBlock, mode, role, msgType string, replyTo int64, clientMsgID string) (int64, bool, error) {
	s.calls++
	s.clientMsgID = clientMsgID
	return s.id, s.duplicate, nil
}
func (s *idempotentMessageStore) GetMessagesSince(topicID string, sinceID int64, limit int) ([]*types.Message, error) {
	return nil, nil
}
func (s *idempotentMessageStore) GetMessages(topicID string, limit, offset int) ([]*types.Message, error) {
	return nil, nil
}
func (s *idempotentMessageStore) GetLatestMessages(topicID string, limit, offset int) ([]*types.Message, error) {
	return nil, nil
}
func (s *idempotentMessageStore) GetLatestMessagesForTopics(topicIDs []string) (map[string]*types.Message, error) {
	return nil, nil
}
