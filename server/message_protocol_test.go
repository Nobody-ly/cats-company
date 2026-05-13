package server

import (
	"encoding/json"
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
