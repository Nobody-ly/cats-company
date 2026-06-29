import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Copy, RefreshCw, X } from 'lucide-react';
import { api } from '../api';
import QRCode from './qr-code';

const MOBILE_CHANNELS = [
  { value: 'weixin', label: '公众号', displayName: '微信公众号' },
  { value: 'feishu', label: '飞书', displayName: '飞书' },
  { value: 'weixin_clawbot', label: 'ClawBot', displayName: '微信 ClawBot' },
];

const channelMeta = (value) => (
  MOBILE_CHANNELS.find((item) => item.value === value) || MOBILE_CHANNELS[0]
);

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
  const activeChannel = channelMeta(channel);
  const isWeixinOfficialQR = channel === 'weixin' && qrKind === 'weixin_official_qr';
  const isFeishuNativeUnconfigured = channel === 'feishu' && qrKind === 'feishu_native_unconfigured';
  const isClawBotIlinkQR = channel === 'weixin_clawbot' && qrKind === 'weixin_clawbot_ilink_qr';
  const isClawBotUnavailable = channel === 'weixin_clawbot' && linkInfo && !isClawBotIlinkQR;
  const shouldSuppressQRCode = (channel === 'weixin' && !isWeixinOfficialQR) || isFeishuNativeUnconfigured || isClawBotUnavailable;
  const qrValue = shouldSuppressQRCode ? '' : (linkInfo?.qr_value || linkInfo?.channel_qr_url || '');
  const channelImageURL = isWeixinOfficialQR ? (linkInfo?.channel_qr_url || '') : '';
  const copyValue = qrValue || '';
  const channelCopy = (() => {
    if (channel === 'weixin' && linkInfo && !isWeixinOfficialQR) {
      return '微信公众号参数二维码尚未配置，暂时不能生成公众号移动端绑定二维码。';
    }
    if (isFeishuNativeUnconfigured) {
      return '飞书原生入口尚未配置，暂时不能生成飞书移动端二维码。';
    }
    if (isClawBotUnavailable) {
      return '微信 ClawBot 授权二维码暂时不可用，请稍后刷新重试。';
    }
    if (channel === 'weixin_clawbot') {
      return '扫码会完成微信 ClawBot 授权；它不会像公众号一样直接进入该机器人聊天框，之后请在微信里打开 ClawBot 对话继续使用。';
    }
    if (isGroupTarget) {
      return `扫码后会把你的${activeChannel.displayName}身份绑定到当前 CatsCo 账号，之后可直接在移动端进入这个群聊。`;
    }
    return `扫码后会把你的${activeChannel.displayName}身份绑定到当前 CatsCo 账号，之后可直接在移动端继续和这个虚拟员工对话。`;
  })();
  const emptyQrText = isFeishuNativeUnconfigured
    ? '飞书原生入口尚未配置，暂时不能生成飞书移动端二维码'
    : isClawBotUnavailable
      ? '微信 ClawBot 授权二维码暂时不可用'
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
          {MOBILE_CHANNELS.map((item) => (
            <button key={item.value} type="button" className={channel === item.value ? 'active' : ''} onClick={() => setChannel(item.value)}>
              {item.label}
            </button>
          ))}
        </div>

        <p className="mobile-channel-copy">{channelCopy}</p>

        <div className="mobile-channel-qr-wrap">
          {loading && <div className="mobile-channel-placeholder">正在生成...</div>}
          {!loading && error && <div className="mobile-channel-error">{error}</div>}
          {!loading && !error && channelImageURL && (
            <img className="mobile-channel-qr-img" src={channelImageURL} alt={`${activeChannel.displayName}移动端绑定二维码`} />
          )}
          {!loading && !error && !channelImageURL && qrValue && (
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
