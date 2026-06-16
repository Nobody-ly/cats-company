package server

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/openchat/openchat/server/store"
	"github.com/openchat/openchat/server/store/types"
)

func deliverInboundChannelTextToAgent(db store.Store, hub *Hub, actorUID, agentUID int64, text, clientMsgID, source string, metadata map[string]interface{}) error {
	if actorUID <= 0 || agentUID <= 0 {
		return errors.New("invalid actor or agent")
	}
	binding, err := resolveDeliverableChannelBinding(db, actorUID, agentUID, metadata)
	if err != nil {
		return err
	}
	agent, err := db.GetUser(agentUID)
	if err != nil || agent == nil || agent.AccountType != types.AccountBot || agent.State != 0 {
		return errors.New("agent unavailable")
	}
	conversationUID := channelBindingConversationActorUID(binding, actorUID)
	if conversationUID <= 0 {
		return errors.New("invalid channel conversation actor")
	}
	metadata = withChannelBindingDeliveryMetadata(metadata, binding)
	topicID := p2pTopicID(conversationUID, agentUID)
	if err := db.CreateTopic(topicID, "p2p", conversationUID); err != nil {
		return fmt.Errorf("create agent topic: %w", err)
	}
	rawContent, _ := json.Marshal(text)
	payload, err := normalizeMessageRequest(&SendMessageRequest{
		TopicID:     topicID,
		ClientMsgID: clientMsgID,
		Content:     rawContent,
		Type:        "text",
		Metadata:    metadata,
	})
	if err != nil {
		return err
	}
	result, err := saveNormalizedMessage(db, topicID, conversationUID, 0, payload)
	if err != nil {
		if source == "" {
			source = "channel"
		}
		return fmt.Errorf("save inbound %s message: %w", source, err)
	}
	if !result.Duplicate && hub != nil {
		hub.recordChannelInboundReplyRoute(topicID, conversationUID, binding)
		hub.fanoutNormalizedMessage(conversationUID, topicID, 0, payload, result.ID, nil)
	}
	return nil
}
