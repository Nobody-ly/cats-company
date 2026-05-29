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

// TestBuildGroupConversationSummary_UsesCreatedAtWhenLatestIsOlder 测试：
// 如果群组最新消息时间早于创建时间，仍使用 created_at 作为排序时间
func TestBuildGroupConversationSummary_UsesCreatedAtWhenLatestIsOlder(t *testing.T) {
	groupCreatedAt := time.Now()
	messageTime := groupCreatedAt.Add(-10 * time.Minute)

	group := &types.Group{
		ID:        3,
		Name:      "New Topic",
		OwnerID:   100,
		CreatedAt: groupCreatedAt,
	}

	latestMsg := &types.Message{
		ID:        1000,
		CreatedAt: messageTime,
		Content:   "Older message",
		MsgType:   "text",
	}

	summary := buildGroupConversationSummary("grp_3", group, latestMsg)

	if summary.LastTime == nil {
		t.Fatal("LastTime should not be nil")
	}
	if !summary.LastTime.Equal(groupCreatedAt) {
		t.Fatalf("expected LastTime=%v (group created time), got %v", groupCreatedAt, summary.LastTime)
	}
	if summary.Preview != "Older message" {
		t.Fatalf("expected Preview=Older message, got %s", summary.Preview)
	}
	if summary.LatestSeq != 1000 {
		t.Fatalf("expected LatestSeq=1000, got %d", summary.LatestSeq)
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

// TestConversationLess_SameCreatedAtUsesGroupIDDesc 测试：
// MySQL 时间戳可能只有秒级精度，同一秒创建的群应使用 ID 确定新群优先
func TestConversationLess_SameCreatedAtUsesGroupIDDesc(t *testing.T) {
	createdAt := time.Now().Truncate(time.Second)

	olderIDGroup := buildGroupConversationSummary("grp_30", &types.Group{
		ID:        30,
		Name:      "Older ID",
		OwnerID:   100,
		CreatedAt: createdAt,
	}, nil)
	newerIDGroup := buildGroupConversationSummary("grp_31", &types.Group{
		ID:        31,
		Name:      "Newer ID",
		OwnerID:   100,
		CreatedAt: createdAt,
	}, nil)

	conversations := []*types.ConversationSummary{olderIDGroup, newerIDGroup}
	sort.SliceStable(conversations, conversationLess(conversations))

	if conversations[0].GroupID != 31 {
		t.Fatalf("expected higher group ID first when timestamps tie, got GroupID=%d", conversations[0].GroupID)
	}
}

// TestConversationLess_SameTimePrefersGroup 测试：
// 同秒情况下，新话题应优先浮到会话区顶部，避免创建后看起来仍在底部
func TestConversationLess_SameTimePrefersGroup(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	groupSummary := buildGroupConversationSummary("grp_40", &types.Group{
		ID:        40,
		Name:      "New Topic",
		OwnerID:   100,
		CreatedAt: now,
	}, nil)
	friendSummary := buildFriendConversationSummary("p2p_100_200", &types.User{
		ID:          200,
		DisplayName: "Friend",
		Username:    "friend",
	}, &types.Message{
		ID:        999,
		TopicID:   "p2p_100_200",
		FromUID:   200,
		Content:   "Same second",
		MsgType:   "text",
		CreatedAt: now,
	}, nil)

	conversations := []*types.ConversationSummary{friendSummary, groupSummary}
	sort.SliceStable(conversations, conversationLess(conversations))

	if conversations[0].GroupID != 40 {
		t.Fatalf("expected group first when timestamps tie, got %#v", conversations[0])
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
