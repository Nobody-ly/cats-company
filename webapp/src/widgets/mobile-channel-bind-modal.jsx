import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Copy, RefreshCw, X } from 'lucide-react';
import { api } from '../api';
import QRCode from './qr-code';

export default function MobileChannelBindModal({ agentUid, agentName, groupId, topicId, groupName, onClose }) {
  const [channel, setChannel] = useState('weixin');
  const [linkInfo, setLinkInfo] = useState(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [copied, setCopied] = useState(false);
  const requestSeqRef = useRef(0);
  const isGroupTarget = Boolean(groupId || topicId);
  const targetName = isGroupTarget ? (groupName || '群聊') : agentName;

  const loadLink = useCallback(async () => {
    if (!isGroupTarget && !agentUid) return;
    const requestSeq = requestSeqRef.current + 1;
    requestSeqRef.current = requestSeq;
    try {
      setLoading(true);
      setError('');
      setCopied(false);
      setLinkInfo(null);
      const res = isGroupTarget
        ? await api.createChannelGroupMobileLink(groupId, topicId, channel)
        : await api.createChannelIdentityMobileLink(agentUid, channel);
      if (requestSeqRef.current !== requestSeq) return;
      setLinkInfo(res);
    } catch (err) {
      if (requestSeqRef.current !== requestSeq) return;
      setLinkInfo(null);
      setError(err.message || '暂时无法生成移动端入口');
    } finally {
      if (requestSeqRef.current === requestSeq) {
        setLoading(false);
      }
    }
  }, [agentUid, channel, groupId, isGroupTarget, topicId]);

  useEffect(() => {
    loadLink();
  }, [loadLink]);

  const qrKind = linkInfo?.qr_kind || linkInfo?.entry?.qr_kind || '';
  const isWeixinOfficialQR = channel === 'weixin' && qrKind === 'weixin_official_qr';
  const isFeishuNativeUnconfigured = channel === 'feishu' && qrKind === 'feishu_native_unconfigured';
  const qrValue = channel === 'weixin' && !isWeixinOfficialQR
    ? ''
    : (linkInfo?.qr_value || linkInfo?.channel_qr_url || '');
  const weixinImageURL = isWeixinOfficialQR ? (linkInfo?.channel_qr_url || '') : '';
  const copyValue = qrValue || '';
  const channelCopy = (() => {
    if (channel === 'weixin' && linkInfo && !isWeixinOfficialQR) {
      return '微信公众号参数二维码尚未配置，暂时不能生成微信移动端绑定二维码。';
    }
    if (isFeishuNativeUnconfigured) {
      return '飞书原生入口尚未配置，暂时不能生成飞书移动端二维码。';
    }
    if (isGroupTarget) {
      return `扫码后会把你的${channel === 'weixin' ? '微信' : '飞书'}身份绑定到当前 CatsCo 账号，之后可直接在移动端进入这个群聊。`;
    }
    return `扫码后会把你的${channel === 'weixin' ? '微信' : '飞书'}身份绑定到当前 CatsCo 账号，之后可直接在移动端继续和这个虚拟员工对话。`;
  })();
  const emptyQrText = isFeishuNativeUnconfigured
    ? '飞书原生入口尚未配置，暂时不能生成飞书移动端二维码'
    : channel === 'weixin' && linkInfo && !isWeixinOfficialQR
      ? '微信公众号参数二维码尚未配置'
      : '暂时没有可用二维码';

  const handleCopy = async () => {
    if (!copyValue || !navigator.clipboard) return;
    try {
      await navigator.clipboard.writeText(copyValue);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch (err) {
      setError('复制失败，请手动复制链接');
    }
  };

  return (
    <div className="oc-modal-overlay">
      <div className="oc-modal mobile-channel-bind-modal">
        <div className="mobile-channel-bind-header">
          <div>
            <div className="oc-modal-title">移动端使用</div>
            <div className="mobile-channel-bind-subtitle">{targetName}</div>
          </div>
          <button type="button" className="v3-action-btn" onClick={onClose} aria-label="关闭">
            <X size={16} />
          </button>
        </div>

        <div className="mobile-channel-tabs">
          <button type="button" className={channel === 'weixin' ? 'active' : ''} onClick={() => setChannel('weixin')}>微信</button>
          <button type="button" className={channel === 'feishu' ? 'active' : ''} onClick={() => setChannel('feishu')}>飞书</button>
        </div>

        <p className="mobile-channel-copy">{channelCopy}</p>

        <div className="mobile-channel-qr-wrap">
          {loading && <div className="mobile-channel-placeholder">正在生成...</div>}
          {!loading && error && <div className="mobile-channel-error">{error}</div>}
          {!loading && !error && weixinImageURL && (
            <img className="mobile-channel-qr-img" src={weixinImageURL} alt={`${channel === 'weixin' ? '微信' : '飞书'}移动端绑定二维码`} />
          )}
          {!loading && !error && !weixinImageURL && qrValue && (
            <div className="mobile-channel-qr-box">
              <QRCode value={qrValue} size={210} />
            </div>
          )}
          {!loading && !error && !qrValue && (
            <div className="mobile-channel-placeholder">{emptyQrText}</div>
          )}
        </div>

        {!loading && !error && qrValue && (
          <p className="mobile-channel-expiry">二维码 10 分钟内有效，完成绑定后会自动失效。</p>
        )}

        <div className="mobile-channel-actions">
          <button type="button" className="oc-btn oc-btn-default" onClick={handleCopy} disabled={!copyValue}>
            <Copy size={14} /> {copied ? '已复制' : '复制链接'}
          </button>
          <button type="button" className="oc-btn oc-btn-default" onClick={loadLink} disabled={loading}>
            <RefreshCw size={14} /> 刷新
          </button>
        </div>
      </div>
    </div>
  );
}
