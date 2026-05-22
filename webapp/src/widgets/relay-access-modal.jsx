import React, { useEffect, useMemo, useState } from 'react';
import { Check, Copy, ExternalLink, KeyRound, Server, X } from 'lucide-react';
import { api } from '../api';

const FALLBACK_CONFIG = {
  base_url: 'https://relay.catsco.cc',
  default_model: 'MiniMax-M2.7',
  endpoints: [
    { protocol: 'OpenAI-compatible', base_url: 'https://relay.catsco.cc/v1' },
    { protocol: 'Anthropic-compatible', base_url: 'https://relay.catsco.cc/anthropic' },
  ],
  key_hint: '访问凭证由 CatsCo 管理员发放，使用 Bifrost Virtual Key。请妥善保存，泄露后可联系管理员撤销并重建。',
  docs_url: 'https://relay.catsco.cc',
};

function protocolLabel(protocol) {
  if (/anthropic/i.test(protocol)) return 'Anthropic SDK';
  if (/openai/i.test(protocol)) return 'OpenAI SDK';
  return protocol;
}

function configSnippet(config) {
  const openAI = config.endpoints?.find((item) => /openai/i.test(item.protocol));
  const anthropic = config.endpoints?.find((item) => /anthropic/i.test(item.protocol));
  return [
    'OpenAI-compatible',
    `Base URL: ${openAI?.base_url || `${config.base_url}/v1`}`,
    `Model: ${config.default_model}`,
    'API Key: sk-bf-...（管理员发放）',
    '',
    'Anthropic-compatible',
    `Base URL: ${anthropic?.base_url || `${config.base_url}/anthropic`}`,
    `Model: ${config.default_model}`,
    'API Key: sk-bf-...（管理员发放）',
  ].join('\n');
}

export default function RelayAccessModal({ onClose }) {
  const [config, setConfig] = useState(FALLBACK_CONFIG);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [copied, setCopied] = useState('');

  useEffect(() => {
    let cancelled = false;
    api.getRelayConfig()
      .then((data) => {
        if (!cancelled) {
          setConfig({
            ...FALLBACK_CONFIG,
            ...data,
            endpoints: Array.isArray(data.endpoints) && data.endpoints.length > 0
              ? data.endpoints
              : FALLBACK_CONFIG.endpoints,
          });
          setError('');
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err.message || '配置读取失败，已显示默认配置');
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const snippet = useMemo(() => configSnippet(config), [config]);

  const copyText = async (key, text) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      window.setTimeout(() => setCopied(''), 1400);
    } catch (err) {
      setError('复制失败，请手动复制');
    }
  };

  return (
    <div className="oc-modal-overlay" onClick={onClose}>
      <div className="oc-modal relay-access-modal" onClick={(event) => event.stopPropagation()}>
        <div className="oc-modal-header relay-access-header">
          <div>
            <h3>CatsCo 中转站</h3>
            <p>用于 OpenAI / Anthropic 兼容客户端，账号和凭证统一由 CatsCo 管理。</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭">
            <X size={18} />
          </button>
        </div>

        <div className="relay-access-body">
          {loading && <div className="oc-settings-secondary">正在读取中转配置...</div>}
          {error && <div className="oc-form-error">{error}</div>}

          <div className="relay-access-summary">
            <span className="relay-access-summary-icon"><Server size={18} /></span>
            <div>
              <div className="relay-access-title">{config.base_url}</div>
              <div className="oc-settings-secondary">默认模型：{config.default_model}</div>
            </div>
          </div>

          <div className="relay-access-list">
            {config.endpoints.map((endpoint) => (
              <div className="relay-access-card" key={`${endpoint.protocol}:${endpoint.base_url}`}>
                <div className="relay-access-card-copy">
                  <div className="relay-access-title">{protocolLabel(endpoint.protocol)}</div>
                  <div className="relay-access-url">{endpoint.base_url}</div>
                </div>
                <button
                  type="button"
                  className="relay-access-copy-btn"
                  onClick={() => copyText(endpoint.protocol, endpoint.base_url)}
                >
                  {copied === endpoint.protocol ? <Check size={16} /> : <Copy size={16} />}
                </button>
              </div>
            ))}
          </div>

          <div className="relay-access-token-note">
            <KeyRound size={16} />
            <span>{config.key_hint}</span>
          </div>

          <div className="relay-access-snippet">
            <div className="relay-access-snippet-head">
              <span>快速配置</span>
              <button type="button" onClick={() => copyText('snippet', snippet)}>
                {copied === 'snippet' ? <Check size={15} /> : <Copy size={15} />}
                复制
              </button>
            </div>
            <pre>{snippet}</pre>
          </div>

          {config.docs_url && (
            <a className="relay-access-doc-link" href={config.docs_url} target="_blank" rel="noopener noreferrer">
              打开中转站页面 <ExternalLink size={14} />
            </a>
          )}
        </div>
      </div>
    </div>
  );
}
