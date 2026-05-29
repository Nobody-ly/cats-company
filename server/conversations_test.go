package server

import (
	"sort"
	"testing"
	"time"

	"github.com/openchat/openchat/server/store/types"
)

// TestBuildGroupConversationSummary_FallbackToCreatedAt 测试：
// 当群组没有消息时，使用 created_at 作为排序时间
func TestBuildGroupConversationSummary_FallbackToCreatedAt(t *testing.T) {
	now := time.Now()
	createdAt := now.Add(-1 * time.Hour) // 群组1小时前创建

	group := &types.Group{
		ID:        1,
		Name:      "Test Group",
		OwnerID:   100,
		CreatedAt: createdAt,
	}

	// 没有消息
	summary := buildGroupConversationSummary("grp_1", group, nil)

	if summary.LastTime == nil {
		t.Fatal("LastTime should not be nil when group has no messages")
	}

	// 验证 LastTime 等于 group.CreatedAt
	if !summary.LastTime.Equal(createdAt) {
		t.Fatalf("expected LastTime=%v, got %v", createdAt, summary.LastTime)
	}

	// 验证其他字段
	if summary.ID != "grp_1" {
		t.Fatalf("expected ID=grp_1, got %s", summary.ID)
	}
	if summary.Name != "Test Group" {
		t.Fatalf("expected Name=Test Group, got %s", summary.Name)
	}
	if !summary.IsGroup {
		t.Fatal("expected IsGroup=true")
	}
	if summary.GroupID != 1 {
		t.Fatalf("expected GroupID=1, got %d", summary.GroupID)
	}
}

// TestBuildGroupConversationSummary_UsesLatestMessageWhenAvailable 测试：
// 当群组有消息时，使用最新消息时间而不是 created_at
func TestBuildGroupConversationSummary_UsesLatestMessageWhenAvailable(t *testing.T) {
	groupCreatedAt := time.Now().Add(-2 * time.Hour)
	messageTime := time.Now().Add(-10 * time.Minute) // 消息更新

	group := &types.Group{
		ID:        2,
		Name:      "Active Group",
		OwnerID:   100,
		CreatedAt: groupCreatedAt,
	}

	latestMsg := &types.Message{
		ID:        999,
		CreatedAt: messageTime,
		Content:   "Hello",
		MsgType:   "text",
	}

	summary := buildGroupConversationSummary("grp_2", group, latestMsg)

	if summary.LastTime == nil {
		t.Fatal("LastTime should not be nil when group has messages")
	}

	// 验证 LastTime 等于消息时间（不是创建时间）
	if !summary.LastTime.Equal(messageTime) {
		t.Fatalf("expected LastTime=%v (message time), got %v", messageTime, summary.LastTime)
	}

	// 消息预览
	if summary.Preview != "Hello" {
		t.Fatalf("expected Preview=Hello, got %s", summary.Preview)
	}
	if summary.LatestSeq != 999 {
		t.Fatalf("expected LatestSeq=999, got %d", summary.LatestSeq)
	}
}

// TestConversationLess_NoMessagesGroupAtTop 测试：
// 验证无消息群组按创建时间降序排列（越新越靠前），复用实际排序函数
func TestConversationLess_NoMessagesGroupAtTop(t *testing.T) {
	now := time.Now()

	// 创建两个群：一个早，一个晚
	olderGroup := &types.Group{
		ID:        10,
		Name:      "Older Group",
		OwnerID:   100,
		CreatedAt: now.Add(-2 * time.Hour),
	}
	newerGroup := &types.Group{
		ID:        11,
		Name:      "Newer Group",
		OwnerID:   100,
		CreatedAt: now.Add(-1 * time.Hour), // 更晚创建
	}

	// 两个群都没有消息
	olderSummary := buildGroupConversationSummary("grp_10", olderGroup, nil)
	newerSummary := buildGroupConversationSummary("grp_11", newerGroup, nil)

	if olderSummary.LastTime == nil || newerSummary.LastTime == nil {
		t.Fatal("both groups should have LastTime")
	}

	// 验证：新群的时间 > 老群的时间
	if !newerSummary.LastTime.After(*olderSummary.LastTime) {
		t.Fatal("newer group should have later LastTime")
	}

	// 使用实际排序函数
	conversations := []*types.ConversationSummary{olderSummary, newerSummary}
	sort.SliceStable(conversations, conversationLess(conversations))

	// 验证排序结果：新群在前
	if conversations[0].GroupID != 11 {
		t.Fatalf("expected newer group first, got groupID=%d", conversations[0].GroupID)
	}
}

// TestConversationLess_MixedWithP2P 测试：
// 验证无消息群组 vs 无消息 P2P 会话的排序，复用实际排序函数
func TestConversationLess_MixedWithP2P(t *testing.T) {
	now := time.Now()

	// 无消息群组
	group := &types.Group{
		ID:        20,
		Name:      "Empty Group",
		OwnerID:   100,
		CreatedAt: now.Add(-30 * time.Minute),
	}
	groupSummary := buildGroupConversationSummary("grp_20", group, nil)

	// 无消息 P2P 好友（模拟）
	friend := &types.User{
		ID:          200,
		DisplayName: "Friend",
		Username:    "friend",
	}
	friendSummary := buildFriendConversationSummary("p2p_100_200", friend, nil, nil)

	// 验证群组的 LastTime 不为 nil（使用 created_at）
	if groupSummary.LastTime == nil {
		t.Fatal("group LastTime should use created_at")
	}

	// 验证 P2P 的 LastTime 为 nil（无消息时没有 fallback）
	if friendSummary.LastTime != nil {
		t.Fatal("P2P LastTime should be nil when no messages")
	}

	// 使用实际排序函数
	conversations := []*types.ConversationSummary{friendSummary, groupSummary}
	sort.SliceStable(conversations, conversationLess(conversations))

	// 验证群组排在 P2P 前面（因为 P2P 的 LastTime 为 nil，会沉到底部）
	if conversations[0].GroupID != 20 {
		t.Fatalf("expected empty group first, got GroupID=%d", conversations[0].GroupID)
	}
}
