package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/openchat/openchat/server/store/types"
)

func TestThinToolRPCRejectsWrongDeviceResultWithoutConsumingPending(t *testing.T) {
	hub := NewHub(nil, nil)
	agent := &Client{
		uid:         42,
		accountType: types.AccountBot,
		bodyID:      "body-agent",
		send:        make(chan []byte, 4),
	}
	target := &Client{
		uid:            7,
		accountType:    types.AccountHuman,
		deviceOwnerUID: 7,
		deviceID:       "alice-laptop",
		send:           make(chan []byte, 4),
	}
	wrong := &Client{
		uid:            8,
		accountType:    types.AccountHuman,
		deviceOwnerUID: 8,
		deviceID:       "bob-laptop",
		send:           make(chan []byte, 4),
	}
	hub.addClient(agent)
	hub.addClient(target)
	hub.addClient(wrong)

	expiresAt := time.Now().Add(time.Minute)
	if !hub.thinToolRPC.add(thinToolRPCPending{
		requestID:      "thin-1",
		requester:      agent,
		requesterRoute: hub.clientRoute(agent),
		targetRoute:    hub.clientRoute(target),
		targetOwnerUID: 7,
		targetDeviceID: "alice-laptop",
		toolName:       "glob",
		createdAt:      time.Now(),
		expiresAt:      expiresAt,
	}) {
		t.Fatal("failed to add thin tool rpc pending request")
	}

	hub.handleThinToolRPCResult(wrong, &MsgThinToolRPC{
		ID:        "wrong-result",
		Type:      thinToolRPCTypeResult,
		RequestID: "thin-1",
		Result:    map[string]interface{}{"ok": true},
	})

	var wrongAck ServerMessage
	decodeQueuedServerMessage(t, wrong.send, &wrongAck)
	if wrongAck.Ctrl == nil || wrongAck.Ctrl.Code != http.StatusForbidden {
		t.Fatalf("wrong device ack = %#v, want 403", wrongAck.Ctrl)
	}
	if _, ok := hub.thinToolRPC.get("thin-1"); !ok {
		t.Fatal("wrong device result consumed pending request")
	}
	if drainOne(agent.send) {
		t.Fatal("requester should not receive result from wrong device")
	}

	hub.handleThinToolRPCResult(target, &MsgThinToolRPC{
		ID:        "target-result",
		Type:      thinToolRPCTypeResult,
		RequestID: "thin-1",
		Result:    map[string]interface{}{"ok": true},
	})

	var result ServerMessage
	decodeQueuedServerMessage(t, agent.send, &result)
	if result.ThinToolRPC == nil || result.ThinToolRPC.RequestID != "thin-1" || result.ThinToolRPC.DeviceID != "alice-laptop" {
		t.Fatalf("requester result = %#v, want thin_tool_rpc result from target", result.ThinToolRPC)
	}
	var targetAck ServerMessage
	decodeQueuedServerMessage(t, target.send, &targetAck)
	if targetAck.Ctrl == nil || targetAck.Ctrl.Code != http.StatusOK {
		t.Fatalf("target ack = %#v, want 200", targetAck.Ctrl)
	}
	if _, ok := hub.thinToolRPC.get("thin-1"); ok {
		t.Fatal("target result should finish pending request")
	}
}
