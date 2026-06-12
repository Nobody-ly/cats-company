import React, { useMemo, useRef, useState } from 'react';
import { AlertCircle, CheckCircle2, Download, Laptop, Loader2, X } from 'lucide-react';
import { api } from '../api';
import { DOWNLOAD_OPTIONS } from './catsco-download-modal';

function detectRecommendedOption() {
  const platform = `${navigator.platform || ''} ${navigator.userAgent || ''}`.toLowerCase();
  if (platform.includes('win')) return DOWNLOAD_OPTIONS.find((option) => option.key === 'windows');
  if (platform.includes('mac')) {
    if (platform.includes('arm') || platform.includes('apple')) {
      return DOWNLOAD_OPTIONS.find((option) => option.key === 'mac-arm');
    }
    return DOWNLOAD_OPTIONS.find((option) => option.key === 'mac-intel');
  }
  if (platform.includes('linux')) return DOWNLOAD_OPTIONS.find((option) => option.key === 'linux-appimage');
  return DOWNLOAD_OPTIONS.find((option) => option.key === 'windows');
}

function findConnectedLocalDevice(devices) {
  return (devices || []).find((device) => device.routable || device.routeConnected);
}

export default function DesktopConnectModal({ onClose, onConnected, onStatusChange }) {
  const [state, setState] = useState('idle');
  const [error, setError] = useState('');
  const [showDownloads, setShowDownloads] = useState(false);
  const [showAdvancedDownloads, setShowAdvancedDownloads] = useState(false);
  const sessionRef = useRef(null);
  const connectedRef = useRef(false);
  const recommendedDownload = useMemo(() => detectRecommendedOption(), []);
  const otherDownloads = DOWNLOAD_OPTIONS.filter((option) => option.key !== recommendedDownload?.key);

  const markState = (next) => {
    setState(next);
    if (next === 'connected') onStatusChange?.('connected');
    else if (next === 'waiting' || next === 'opening') onStatusChange?.('checking');
    else onStatusChange?.('disconnected');
  };

  const finishIfConnected = async () => {
    const res = await api.getDevices();
    const connected = findConnectedLocalDevice(res.devices || []);
    if (!connected) return false;
    if (connectedRef.current) return true;
    connectedRef.current = true;
    setShowDownloads(false);
    markState('connected');
    window.setTimeout(() => {
      if (onConnected) onConnected(connected);
    }, 600);
    return true;
  };

  const pollForConnection = () => {
    let attempts = 0;
    const timer = window.setInterval(async () => {
      attempts += 1;
      try {
        const session = sessionRef.current;
        if (session?.code) {
          const status = await api.getDesktopConnectStatus(session.code).catch(() => null);
          if (status?.state === 'claimed') {
            markState('waiting');
          }
        }
        const connected = await finishIfConnected();
        if (connected) {
          window.clearInterval(timer);
          return;
        }
      } catch (err) {
        console.warn('Desktop connect poll failed:', err);
      }
      if (attempts >= 8) {
        window.clearInterval(timer);
        if (!connectedRef.current) {
          setShowDownloads(true);
          markState('download');
        }
      }
    }, 2000);
  };

  const startConnect = async () => {
    setError('');
    setShowDownloads(false);
    markState('opening');
    try {
      const alreadyConnected = await finishIfConnected();
      if (alreadyConnected) return;

      const session = await api.createDesktopConnectSession();
      sessionRef.current = session;
      markState('waiting');
      window.location.href = session.deeplink_url || `catsco://connect?code=${encodeURIComponent(session.code)}`;
      pollForConnection();
      window.setTimeout(() => {
        if (connectedRef.current) return;
        setShowDownloads(true);
        setState((current) => (current === 'waiting' || current === 'opening' ? 'waiting_download' : current));
      }, 3000);
    } catch (err) {
      setError(err.message || '连接失败，请稍后重试。');
      setShowDownloads(true);
      markState('failed');
    }
  };

  const renderDownload = (option, primary = false) => {
    const Icon = option.icon;
    return (
      <a
        key={option.key}
        className={`catsco-download-card ${primary ? 'catsco-download-card-primary' : ''}`}
        href={option.href}
        target="_blank"
        rel="noopener noreferrer"
      >
        <span className="catsco-download-icon"><Icon size={20} /></span>
        <span className="catsco-download-copy">
          <span className="catsco-download-title">{option.title}</span>
          <span className="catsco-download-desc">{option.description}</span>
        </span>
        <span className="catsco-download-action"><Download size={16} /></span>
      </a>
    );
  };

  const busy = state === 'opening' || state === 'waiting' || state === 'waiting_download';

  return (
    <div className="oc-modal-overlay" onClick={onClose}>
      <div className="oc-modal catsco-download-modal" onClick={(event) => event.stopPropagation()}>
        <div className="oc-modal-header catsco-download-header">
          <div>
            <h3>连接本地 CatsCo 助手</h3>
            <p>网页登录后，桌面端会自动完成同账号连接。</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭">
            <X size={18} />
          </button>
        </div>

        <div style={{ display: 'grid', gap: 14 }}>
          <div style={{ display: 'flex', gap: 12, alignItems: 'center', color: 'var(--v3-text-main)' }}>
            {state === 'connected' ? <CheckCircle2 size={20} color="#0BA36D" /> : busy ? <Loader2 className="catsco-spin" size={20} /> : <Laptop size={20} />}
            <div>
              <div style={{ fontWeight: 600 }}>
                {state === 'connected' ? '已连接本地助手' : busy ? '正在等待桌面端确认' : '打开已安装的 CatsCo 桌面端'}
              </div>
              <div style={{ fontSize: 13, color: 'var(--v3-text-muted)', marginTop: 4 }}>
                {state === 'download' || state === 'waiting_download'
                  ? '没有检测到已连接的桌面端。若尚未安装，请下载推荐版本；安装后再点击打开。'
                  : '点击后浏览器可能会询问是否允许打开 CatsCo，请选择允许。'}
              </div>
            </div>
          </div>

          {state === 'waiting_download' && (
            <div className="catsco-connect-hint">
              <AlertCircle size={16} />
              <span>如果浏览器没有弹出打开确认，通常表示这台电脑还没安装新版 CatsCo，或当前版本不支持快捷连接。</span>
            </div>
          )}

          {error && <div style={{ color: '#FA5151', fontSize: 13 }}>{error}</div>}

          {state === 'connected' && (
            <div style={{ color: '#0BA36D', fontSize: 13, fontWeight: 600 }}>
              已连接到本地 CatsCo 桌面助手，正在为你打开对话。
            </div>
          )}

          <button className="oc-btn oc-btn-primary" type="button" onClick={startConnect} disabled={state === 'connected' || busy}>
            {busy ? <Loader2 className="catsco-spin" size={16} style={{ marginRight: 8 }} /> : <Laptop size={16} style={{ marginRight: 8 }} />}
            {busy ? '等待连接...' : '打开 CatsCo 桌面端'}
          </button>

          <button
            type="button"
            className="oc-btn oc-btn-default"
            style={{ width: '100%', justifyContent: 'center' }}
            onClick={() => setShowDownloads((value) => !value)}
          >
            {showDownloads ? '收起下载' : '下载桌面端'}
          </button>

          {showDownloads && (
            <div className="catsco-download-list">
              {recommendedDownload && renderDownload(recommendedDownload, true)}
              <button
                type="button"
                className="oc-btn oc-btn-default"
                style={{ width: '100%', justifyContent: 'center' }}
                onClick={() => setShowAdvancedDownloads((value) => !value)}
              >
                {showAdvancedDownloads ? '收起其他版本' : '其他系统版本'}
              </button>
              {showAdvancedDownloads && otherDownloads.map((option) => renderDownload(option))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
