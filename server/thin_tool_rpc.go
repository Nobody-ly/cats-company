package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	thinToolRPCTypeRequest = "request"
	thinToolRPCTypeResult  = "result"
	defaultThinToolRPCTTL   = 60 * time.Second
)

type thinToolRPCPending struct {
	requestID      string
	requester      *Client
	requesterRoute runtimeRoute
	targetOwnerUID int64
	targetDeviceID string
	toolName       string
	createdAt      time.Time
	expiresAt      time.Time
}

type thinToolRPCRouter struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	pending map[string]thinToolRPCPending
}

func newThinToolRPCRouter(ttl time.Duration) *thinToolRPCRouter {
	if ttl <= 0 {
		ttl = defaultThinToolRPCTTL
	}
	return &thinToolRPCRouter{
		ttl:     ttl,
		now:     time.Now,
		pending: make(map[string]thinToolRPCPending),
	}
}

func (r *thinToolRPCRouter) add(pending thinToolRPCPending) bool {
	if r == nil || pending.requestID == "" || pending.expiresAt.IsZero() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.pending[pending.requestID]; exists {
		return false
	}
	r.pending[pending.requestID] = pending
	return true
}

func (r *thinToolRPCRouter) finish(requestID string) (thinToolRPCPending, bool) {
	if r == nil || requestID == "" {
		return thinToolRPCPending{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.pending[requestID]
	if ok {
		delete(r.pending, requestID)
	}
	return pending, ok
}

func (r *thinToolRPCRouter) expire(now time.Time) []thinToolRPCPending {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var expired []thinToolRPCPending
	for requestID, pending := range r.pending {
		if !now.Before(pending.expiresAt) {
			expired = append(expired, pending)
			delete(r.pending, requestID)
		}
	}
	return expired
}

func (h *Hub) handleThinToolRPC(client *Client, msg *MsgThinToolRPC) {
	switch strings.ToLower(strings.TrimSpace(msg.Type)) {
	case thinToolRPCTypeRequest:
		h.handleThinToolRPCRequest(client, msg)
	case thinToolRPCTypeResult:
		h.handleThinToolRPCResult(client, msg)
	default:
		h.sendThinToolRPCAck(client, msg.ID, http.StatusBadRequest, "unknown thin_tool_rpc type", nil)
	}
}

func (h *Hub) handleThinToolRPCRequest(client *Client, msg *MsgThinToolRPC) {
	if h == nil || h.thinToolRPC == nil {
		h.sendThinToolRPCAck(client, msg.ID, http.StatusServiceUnavailable, "thin tool rpc unavailable", nil)
		return
	}
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		h.sendThinToolRPCAck(client, msg.ID, http.StatusBadRequest, "request_id required", nil)
		return
	}
	ownerUID := parseFormattedUID(msg.TargetOwnerUserID)
	deviceID := strings.TrimSpace(msg.TargetDeviceID)
	toolName := strings.TrimSpace(msg.ToolName)
	if ownerUID <= 0 || deviceID == "" || toolName == "" {
		h.sendThinToolRPCResultToRequester(client, requestID, msg, "invalid_target", "thin_tool_rpc requires target_owner_user_id, target_device_id, and tool_name")
		h.sendThinToolRPCAck(client, msg.ID, http.StatusOK, "ok", map[string]interface{}{"request_id": requestID})
		return
	}

	route, _ := h.findDeviceRPCTarget(ownerUID, UserDevice{DeviceID: deviceID})
	if !route.validAt(nowForRoute(h)) || !h.routeConnected(route) {
		h.sendThinToolRPCResultToRequester(client, requestID, msg, "target_device_unavailable", fmt.Sprintf("target device %s for %s is not online or has no route", deviceID, formatUID(ownerUID)))
		h.sendThinToolRPCAck(client, msg.ID, http.StatusOK, "ok", map[string]interface{}{"request_id": requestID})
		return
	}

	now := h.thinToolRPC.now()
	expiresAt := now.Add(h.thinToolRPC.ttl)
	if msg.ExpiresAt > 0 {
		if requested := time.UnixMilli(msg.ExpiresAt); requested.Before(expiresAt) {
			expiresAt = requested
		}
	}
	requesterRoute := h.clientRoute(client)
	requesterRoute.ExpiresAt = expiresAt
	forward := *msg
	forward.ID = ""
	forward.Type = thinToolRPCTypeRequest
	forward.RequestID = requestID
	forward.TargetOwnerUserID = formatUID(ownerUID)
	forward.TargetDeviceID = deviceID
	forward.DeviceID = deviceID
	forward.CreatedAt = unixMillis(now)
	forward.ExpiresAt = unixMillis(expiresAt)

	if !h.thinToolRPC.add(thinToolRPCPending{
		requestID:      requestID,
		requester:      client,
		requesterRoute: requesterRoute,
		targetOwnerUID: ownerUID,
		targetDeviceID: deviceID,
		toolName:       toolName,
		createdAt:      now,
		expiresAt:      expiresAt,
	}) {
		h.sendThinToolRPCResultToRequester(client, requestID, msg, "request_id_duplicate", "thin_tool_rpc request_id is already pending")
		h.sendThinToolRPCAck(client, msg.ID, http.StatusOK, "ok", map[string]interface{}{"request_id": requestID})
		return
	}

	if !h.sendThinToolRPCToRoute(route, &forward) {
		h.thinToolRPC.finish(requestID)
		h.sendThinToolRPCResultToRequester(client, requestID, msg, "target_device_unavailable", fmt.Sprintf("target device %s route disappeared before forwarding", deviceID))
		h.sendThinToolRPCAck(client, msg.ID, http.StatusOK, "ok", map[string]interface{}{"request_id": requestID})
		return
	}
	h.sendThinToolRPCAck(client, msg.ID, http.StatusOK, "ok", map[string]interface{}{"request_id": requestID, "target_device_id": deviceID})
}

func (h *Hub) handleThinToolRPCResult(client *Client, msg *MsgThinToolRPC) {
	if h == nil || h.thinToolRPC == nil {
		h.sendThinToolRPCAck(client, msg.ID, http.StatusServiceUnavailable, "thin tool rpc unavailable", nil)
		return
	}
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		h.sendThinToolRPCAck(client, msg.ID, http.StatusBadRequest, "request_id required", nil)
		return
	}
	pending, ok := h.thinToolRPC.finish(requestID)
	if !ok {
		h.sendThinToolRPCAck(client, msg.ID, http.StatusNotFound, "request not pending", map[string]interface{}{"request_id": requestID})
		return
	}
	forward := *msg
	forward.ID = ""
	forward.Type = thinToolRPCTypeResult
	forward.RequestID = requestID
	forward.TargetOwnerUserID = formatUID(pending.targetOwnerUID)
	forward.TargetDeviceID = pending.targetDeviceID
	forward.DeviceID = firstNonEmpty(forward.DeviceID, pending.targetDeviceID)
	forward.ToolName = firstNonEmpty(forward.ToolName, pending.toolName)
	if !h.sendThinToolRPCToRoute(pending.requesterRoute, &forward) {
		if h.isClientRegistered(pending.requester) {
			h.SendToClient(pending.requester, &ServerMessage{ThinToolRPC: &forward})
		} else {
			h.sendThinToolRPCAck(client, msg.ID, http.StatusGone, "requester offline", map[string]interface{}{"request_id": requestID})
			return
		}
	}
	h.sendThinToolRPCAck(client, msg.ID, http.StatusOK, "ok", map[string]interface{}{"request_id": requestID})
}

func (h *Hub) expireThinToolRPCRequests(now time.Time) int {
	if h == nil || h.thinToolRPC == nil {
		return 0
	}
	expired := h.thinToolRPC.expire(now)
	for _, pending := range expired {
		h.notifyThinToolRPCTimeout(pending)
	}
	return len(expired)
}

func (h *Hub) notifyThinToolRPCTimeout(pending thinToolRPCPending) {
	if h == nil || pending.requestID == "" {
		return
	}
	msg := &MsgThinToolRPC{
		Type:              thinToolRPCTypeResult,
		RequestID:         pending.requestID,
		TargetOwnerUserID: formatUID(pending.targetOwnerUID),
		TargetDeviceID:    pending.targetDeviceID,
		DeviceID:          pending.targetDeviceID,
		ToolName:          pending.toolName,
		Error: &MsgDeviceRPCError{
			Code:    "thin_tool_rpc_timeout",
			Message: "target device did not return a tool result before the request expired",
		},
		CreatedAt: unixMillis(pending.createdAt),
		ExpiresAt: unixMillis(pending.expiresAt),
	}
	_ = h.sendThinToolRPCToRoute(pending.requesterRoute, msg)
}

func (h *Hub) sendThinToolRPCResultToRequester(client *Client, requestID string, request *MsgThinToolRPC, code string, message string) {
	if client == nil || requestID == "" {
		return
	}
	h.SendToClient(client, &ServerMessage{ThinToolRPC: &MsgThinToolRPC{
		Type:              thinToolRPCTypeResult,
		RequestID:         requestID,
		TargetOwnerUserID: request.TargetOwnerUserID,
		TargetDeviceID:    request.TargetDeviceID,
		DeviceID:          request.TargetDeviceID,
		ToolName:          request.ToolName,
		Error:             &MsgDeviceRPCError{Code: code, Message: message},
		CreatedAt:         unixMillis(time.Now()),
	}})
}

func (h *Hub) sendThinToolRPCToLocalRoute(route runtimeRoute, msg *MsgThinToolRPC) bool {
	if h == nil || route.ConnectionID == "" {
		return false
	}
	client := h.getClientByConnectionID(route.ConnectionID)
	if client == nil {
		return false
	}
	h.SendToClient(client, &ServerMessage{ThinToolRPC: msg})
	return true
}

func (h *Hub) sendThinToolRPCToRoute(route runtimeRoute, msg *MsgThinToolRPC) bool {
	if h == nil || !route.validAt(nowForRoute(h)) || msg == nil {
		return false
	}
	if route.NodeID == "" || route.NodeID == h.nodeID {
		return h.sendThinToolRPCToLocalRoute(route, msg)
	}
	if h.sharedRuntime != nil {
		return h.sharedRuntime.deliverThinToolRPC(route, msg, nowForRoute(h))
	}
	return false
}

func (h *Hub) sendThinToolRPCAck(client *Client, id string, code int, text string, params map[string]interface{}) {
	h.SendToClient(client, &ServerMessage{
		Ctrl: &MsgServerCtrl{
			ID:     id,
			Code:   code,
			Text:   text,
			Params: params,
		},
	})
}
