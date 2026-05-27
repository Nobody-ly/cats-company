import React, { act } from 'react';
import { createRoot } from 'react-dom/client';
import { Simulate } from 'react-dom/test-utils';

jest.mock('../widgets/chat-message', () => function MockChatMessage() {
  return null;
});

jest.mock('../widgets/group-settings', () => function MockGroupSettings() {
  return null;
});

jest.mock('../widgets/avatar', () => function MockAvatar() {
  return null;
});

jest.mock('../api', () => ({
  api: {
    getMessages: jest.fn(),
    getFriends: jest.fn(),
    getGroupInfo: jest.fn(),
    sendMessage: jest.fn(),
  },
  wsSendMessage: jest.fn(),
  wsSendStreamCancel: jest.fn(),
  wsSendTyping: jest.fn(),
  wsSendRead: jest.fn(),
  onWSMessage: jest.fn(() => jest.fn()),
  updateTopicSeq: jest.fn(),
}));

const MessagesView = require('./messages-view').default;
const { api, onWSMessage } = require('../api');

const user = {
  uid: 1,
  username: 'me',
  display_name: 'Me',
  avatar_url: '',
  account_type: 'human',
};

function renderTopic(root, topic) {
  root.render(
    <MessagesView
      topic={topic}
      topicName={topic}
      user={user}
      isGroup={false}
      groupId={null}
      topicAvatarUrl=""
      onTopicUpdated={jest.fn()}
    />
  );
}

async function mountTopic(root, topic) {
  await act(async () => {
    renderTopic(root, topic);
    await Promise.resolve();
  });
}

function typeDraft(textarea, value) {
  textarea.value = value;
  Simulate.change(textarea, {
    target: {
      value,
      selectionStart: value.length,
    },
  });
}

describe('MessagesView composer draft isolation', () => {
  let container;
  let root;

  beforeEach(() => {
    global.IS_REACT_ACT_ENVIRONMENT = true;
    api.getMessages.mockResolvedValue({ messages: [] });
    api.getFriends.mockResolvedValue({ friends: [] });
    api.getGroupInfo.mockResolvedValue({ members: [], group: null });
    api.sendMessage.mockResolvedValue({ seq_id: 100 });
    onWSMessage.mockImplementation(() => jest.fn());

    window.HTMLElement.prototype.scrollIntoView = jest.fn();
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

  it('clears an unsent draft when switching topics', async () => {
    await mountTopic(root, 'p2p_1_2');

    const textarea = container.querySelector('textarea.v3-composer-input');
    await act(async () => {
      typeDraft(textarea, 'do not carry me');
    });

    expect(textarea.value).toBe('do not carry me');

    await mountTopic(root, 'p2p_1_3');

    expect(container.querySelector('textarea.v3-composer-input').value).toBe('');
  });

  it('does not restore a failed old-topic draft after the user has switched topics', async () => {
    let rejectSend;
    api.sendMessage.mockImplementationOnce(() => new Promise((resolve, reject) => {
      rejectSend = reject;
    }));

    await mountTopic(root, 'p2p_1_2');

    const textarea = container.querySelector('textarea.v3-composer-input');
    await act(async () => {
      typeDraft(textarea, 'old topic draft');
    });

    await act(async () => {
      Simulate.click(container.querySelector('button.v3-send'));
    });

    expect(container.querySelector('textarea.v3-composer-input').value).toBe('');

    await mountTopic(root, 'p2p_1_3');

    await act(async () => {
      rejectSend(new Error('send failed'));
      await Promise.resolve();
    });

    expect(container.querySelector('textarea.v3-composer-input').value).toBe('');
  });
});
