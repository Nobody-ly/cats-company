import React from 'react';
import { Apple, Download, Laptop, Monitor, X } from 'lucide-react';

const RELEASE_VERSION = '1.1.5';
const TOS_BASE_URL = 'https://github-release.tos-cn-guangzhou.volces.com/update';

const DOWNLOAD_OPTIONS = [
  {
    key: 'windows',
    title: 'Windows',
    description: '适用于 Windows 10/11 的安装程序',
    icon: Monitor,
    href: `${TOS_BASE_URL}/CatsCo-${RELEASE_VERSION}-win.exe`,
    meta: 'x64 / arm64 由安装包自动适配',
  },
  {
    key: 'mac-arm',
    title: 'macOS Apple Silicon',
    description: '适用于 M 系列芯片 Mac',
    icon: Apple,
    href: `${TOS_BASE_URL}/macos-arm64/CatsCo-${RELEASE_VERSION}-mac.dmg`,
    meta: 'arm64',
  },
  {
    key: 'mac-intel',
    title: 'macOS Intel',
    description: '适用于 Intel 芯片 Mac',
    icon: Apple,
    href: `${TOS_BASE_URL}/macos-x64/CatsCo-${RELEASE_VERSION}-mac.dmg`,
    meta: 'x64',
  },
  {
    key: 'linux-appimage',
    title: 'Linux AppImage',
    description: '无需安装，下载后赋予执行权限运行',
    icon: Laptop,
    href: `${TOS_BASE_URL}/CatsCo-${RELEASE_VERSION}-linux.AppImage`,
    meta: 'x64',
  },
  {
    key: 'linux-deb',
    title: 'Linux Debian / Ubuntu',
    description: '适用于 Debian、Ubuntu 等发行版',
    icon: Laptop,
    href: `${TOS_BASE_URL}/CatsCo-${RELEASE_VERSION}-linux.deb`,
    meta: 'deb',
  },
];

export default function CatsCoDownloadModal({ onClose }) {
  return (
    <div className="oc-modal-overlay" onClick={onClose}>
      <div className="oc-modal catsco-download-modal" onClick={(event) => event.stopPropagation()}>
        <div className="oc-modal-header catsco-download-header">
          <div>
            <h3>下载 CatsCo 桌面端</h3>
            <p>当前版本 v{RELEASE_VERSION}，文件托管在广州 TOS 桶。</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭">
            <X size={18} />
          </button>
        </div>

        <div className="catsco-download-list">
          {DOWNLOAD_OPTIONS.map((option) => {
            const Icon = option.icon;
            return (
              <a
                key={option.key}
                className="catsco-download-card"
                href={option.href}
                target="_blank"
                rel="noopener noreferrer"
              >
                <span className="catsco-download-icon">
                  <Icon size={20} />
                </span>
                <span className="catsco-download-copy">
                  <span className="catsco-download-title">{option.title}</span>
                  <span className="catsco-download-desc">{option.description}</span>
                </span>
                <span className="catsco-download-meta">{option.meta}</span>
                <span className="catsco-download-action">
                  <Download size={16} />
                </span>
              </a>
            );
          })}
        </div>
      </div>
    </div>
  );
}
