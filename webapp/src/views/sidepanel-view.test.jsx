import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { Simulate } from 'react-dom/test-utils';

jest.mock('../widgets/create-group', () => function MockCreateGroup() {
  return null;
});

jest.mock('../widgets/add-friend', () => function MockAddFriend() {
  return null;
});

jest.mock('../widgets/friend-request', () => function MockFriendRequest() {
  return null;
});

jest.mock('../widgets/agent-store-modal', () => function MockAgentStoreModal() {
  return null;
});

jest.mock('../api', () => ({
  api: {
    getConversations: jest.fn(),
    getFriends: jest.fn(),
    getGroups: jest.fn(),
    getPendingRequests: jest.fn(),
    getAgents: jest.fn(),
    openAgent: jest.fn(),
    acceptFriend: jest.fn(),
    rejectFriend: jest.fn(),
    disbandGroup: jest.fn(),
  },
  onWSMessage: jest.fn(() => jest.fn()),
  updateTopicSeq: jest.fn(),
}));

const ChatListView = require('./sidepanel-view').default;
const { api, onWSMessage } = require('../api');

const user = {
  uid: 7,
  username: 'bruce',
  display_name: '布鲁斯',
};

describe('ChatListView virtual employees', () => {
  let container;
  let root;
  let onSelectTopic;

  beforeEach(() => {
    global.IS_REACT_ACT_ENVIRONMENT = true;
    api.getConversations.mockResolvedValue({ conversations: [] });
    api.getFriends.mockResolvedValue({ friends: [] });
    api.getGroups.mockResolvedValue({ groups: [] });
    api.getPendingRequests.mockResolvedValue({ requests: [] });
    api.getAgents.mockResolvedValue({
      agents: [
        {
          id: 42,
          uid: 42,
          username: 'dev-agent',
          display_name: 'Dev Agent',
          avatar_url: '/uploads/dev.png',
          topic_id: 'p2p_7_42',
          is_online: true,
        },
      ],
    });
    api.openAgent.mockResolvedValue({
      agent: {
        uid: 42,
        display_name: 'Dev Agent',
        avatar_url: '/uploads/dev.png',
        topic_id: 'p2p_7_42',
      },
      topic: 'p2p_7_42',
    });
    onWSMessage.mockImplementation(() => jest.fn());
    onSelectTopic = jest.fn();

    container = document.createElement('div');
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    container.remove();
    jest.clearAllMocks();
  });

  async function mount(props = {}) {
    await act(async () => {
      root.render(
        <ChatListView
          activeTopic={null}
          onSelectTopic={onSelectTopic}
          user={user}
          onlineUsers={{}}
          {...props}
        />
      );
      await Promise.resolve();
    });
  }

  it('opens a platform-confirmed agent chat from the virtual employees roster', async () => {
    await mount();

    expect(container.textContent).toContain('Virtual Employees');
    expect(container.textContent).toContain('Dev Agent');

    await act(async () => {
      Simulate.click(container.querySelector('.v3-chat-item'));
      await Promise.resolve();
    });

    expect(api.openAgent).toHaveBeenCalledWith(42);
    expect(onSelectTopic).toHaveBeenCalledWith({
      topicId: 'p2p_7_42',
      name: 'Dev Agent',
      isGroup: false,
      avatar_url: '/uploads/dev.png',
      friendId: 42,
      isBot: true,
    });
  });

  it('keeps group conversations in the groups section by default', async () => {
    api.getConversations.mockResolvedValue({
      conversations: [
        {
          id: 'grp_9',
          group_id: 9,
          name: '查云端log',
          is_group: true,
          latest_seq: 88,
        },
        {
          id: 'p2p_7_42',
          friend_id: 42,
          name: 'Dev Agent',
          is_group: false,
          is_bot: true,
        },
      ],
    });
    api.getGroups.mockResolvedValue({
      groups: [
        {
          id: 9,
          name: '查云端log',
          owner_id: 11,
        },
      ],
    });

    await mount();

    const sections = Array.from(container.querySelectorAll('.v3-chat-section')).map((node) => node.textContent);
    expect(sections.join(' | ')).toContain('Groups');
    expect(sections.findIndex((text) => text.includes('Groups'))).toBeLessThan(
      sections.findIndex((text) => text.includes('Virtual Employees'))
    );

    const groupItem = Array.from(container.querySelectorAll('.v3-chat-item'))
      .find((node) => node.textContent.includes('查云端log'));
    expect(groupItem).toBeTruthy();

    await act(async () => {
      Simulate.click(groupItem);
      await Promise.resolve();
    });

    expect(onSelectTopic).toHaveBeenCalledWith({
      topicId: 'grp_9',
      name: '查云端log',
      isGroup: true,
      groupId: 9,
      avatar_url: undefined,
    });
  });

  it('lets live offline status override stale agent API online state', async () => {
    await mount({ onlineUsers: { 42: false } });

    const agentItem = Array.from(container.querySelectorAll('.v3-chat-item'))
      .find((node) => node.textContent.includes('Dev Agent'));
    expect(agentItem).toBeTruthy();
    expect(agentItem.querySelector('[aria-label="Offline"]')).toBeTruthy();
    expect(agentItem.querySelector('[aria-label="Online"]')).toBeFalsy();
  });
});
