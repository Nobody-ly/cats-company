import React, { act } from 'react';
import { createRoot } from 'react-dom/client';

jest.mock('../api', () => ({
  api: {
    getRelayConfig: jest.fn(),
    getRelayKey: jest.fn(),
    getRelayCommercial: jest.fn(),
    createRelaySession: jest.fn(),
    createRelayKey: jest.fn(),
    rotateRelayKey: jest.fn(),
    revealRelayKey: jest.fn(),
    revokeRelayKey: jest.fn(),
    redeemRelayInvite: jest.fn(),
  },
}));

const RelayAccessModal = require('./relay-access-modal').default;
const { api } = require('../api');

describe('RelayAccessModal commercial rollout', () => {
  let container;
  let root;

  beforeEach(() => {
    global.IS_REACT_ACT_ENVIRONMENT = true;
    api.getRelayConfig.mockResolvedValue({
      base_url: 'https://relay.catsco.cc',
      default_model: 'MiniMax-M3',
      self_service_enabled: false,
      endpoints: [
        { protocol: 'Anthropic-compatible', base_url: 'https://relay.catsco.cc/anthropic' },
        { protocol: 'OpenAI-compatible', base_url: 'https://relay.catsco.cc/v1' },
      ],
    });
    api.getRelayKey.mockResolvedValue({ key: null });
    api.getRelayCommercial.mockResolvedValue({
      enabled: false,
      note: '套餐和邀请码仍在内部测试。',
      summary: {
        uid: 38,
        total_cny: 0,
        totals_by_model: {},
        entitlements: [],
        plans: [],
      },
    });
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

  async function renderModal() {
    await act(async () => {
      root.render(<RelayAccessModal onClose={jest.fn()} />);
      await Promise.resolve();
      await Promise.resolve();
    });
  }

  it('keeps invite redemption hidden while commercial rollout is disabled', async () => {
    await renderModal();

    expect(container.textContent).toContain('套餐与邀请码');
    expect(container.textContent).toContain('未开放');
    expect(container.textContent).toContain('当前仍使用默认 relay 额度和现有 Key');
    expect(container.textContent).toContain('套餐和邀请码仍在内部测试');
    expect(container.querySelector('.relay-access-invite-form')).toBeNull();
  });

  it('shows invite redemption and per-model budgets when commercial rollout is enabled', async () => {
    api.getRelayCommercial.mockResolvedValue({
      enabled: true,
      summary: {
        uid: 38,
        total_cny: 600,
        totals_by_model: {
          'MiniMax-M3': 500,
          'deepseek-v4-flash': 100,
        },
        entitlements: [
          { id: 1, state: 'active', plan_name: '教师试用包', expires_at: '2026-07-29T00:00:00Z' },
          { state: 'expired', plan_name: '旧套餐' },
        ],
        plans: [
          {
            id: 1,
            slug: 'teacher-trial',
            name: '教师试用包',
            state: 0,
            duration_days: 30,
            sort_order: 10,
            model_budgets: {
              'MiniMax-M3': 500,
              'deepseek-v4-flash': 100,
            },
          },
          {
            id: 2,
            slug: 'disabled',
            name: '禁用套餐',
            state: 1,
            duration_days: 30,
            sort_order: 20,
            model_budgets: {
              'glm-5.1': 500,
            },
          },
        ],
      },
    });

    await renderModal();

    expect(container.textContent).toContain('账本灰度');
    expect(container.textContent).toContain('套餐账本额度');
    expect(container.textContent).toContain('需要管理员后台对账/同步后');
    expect(container.textContent).toContain('当前有效套餐');
    expect(container.textContent).toContain('最近到期');
    expect(container.textContent).toContain('当前套餐');
    expect(container.textContent).toContain('教师试用包');
    expect(container.textContent).toContain('MiniMax-M3');
    expect(container.textContent).toContain('deepseek-v4-flash');
    expect(container.textContent).not.toContain('禁用套餐');
    expect(container.querySelector('.relay-access-invite-form')).not.toBeNull();
  });
});
