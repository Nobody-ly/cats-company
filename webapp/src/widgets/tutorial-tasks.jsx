import React, { useState } from 'react';
import { ChevronRight, Download, FileImage, FolderUp, X } from 'lucide-react';

export const TUTORIAL_TASKS = [
  {
    id: 'read-image',
    title: '读图提取信息',
    description: '从示例图片里整理要点',
    detail: 'CatsCo 会读取一张示例图片，整理图片里的主要内容、可识别文字和可用信息。',
    mediaName: 'catsco-tutorial-sample.png',
    mediaUrl: '/demo-artifacts/catsco-tutorial-sample.png',
    requiresDesktop: true,
    icon: FileImage,
    prompt: '请在我的下载文件夹中找到“catsco-tutorial-sample.png”，读取这张图片的内容，并帮我整理成清晰的要点。请生成一份简短说明，包含：图片里主要有什么、可以提取出的文字或信息、以及你建议我如何使用这份信息。',
  },
  {
    id: 'move-image',
    title: '移动文件到桌面',
    description: '把下载的示例图片移动到桌面',
    detail: 'CatsCo 会在下载文件夹中找到示例图片，并把它安全地移动到桌面。',
    mediaName: 'catsco-tutorial-sample.png',
    mediaUrl: '/demo-artifacts/catsco-tutorial-sample.png',
    requiresDesktop: true,
    icon: FolderUp,
    prompt: '请在我的下载文件夹中找到“catsco-tutorial-sample.png”，把它移动到桌面。完成后告诉我你移动前后的文件位置。如果桌面上已经有同名文件，请不要覆盖，改用一个安全的新文件名。',
  },
];

export function TutorialEmptyState({ onSelectTask, onDismiss }) {
  return (
    <div className="cc-tutorial-empty">
      <div className="cc-tutorial-empty-kicker">试一个文件任务</div>
      <div className="cc-tutorial-empty-desc">下载一份示例文件，让 CatsCo 演示读图和整理本机文件。</div>
      <div className="cc-tutorial-task-grid">
        {TUTORIAL_TASKS.map((task) => (
          <TutorialTaskCard key={task.id} task={task} onClick={() => onSelectTask(task)} />
        ))}
      </div>
      <button type="button" className="cc-tutorial-skip" onClick={onDismiss}>
        暂时不用
      </button>
    </div>
  );
}

export function TutorialTaskPicker({ onClose, onSelectTask }) {
  return (
    <div className="oc-modal-overlay" onClick={onClose}>
      <div className="oc-modal catsco-download-modal cc-tutorial-modal" onClick={(event) => event.stopPropagation()}>
        <div className="oc-modal-header catsco-download-header">
          <div>
            <h3>选择示例任务</h3>
            <p>每个任务都带有示例文件和写好的任务说明，你只需要确认发送。</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭">
            <X size={18} />
          </button>
        </div>
        <div className="cc-tutorial-task-list">
          {TUTORIAL_TASKS.map((task) => (
            <TutorialTaskCard key={task.id} task={task} onClick={() => onSelectTask(task)} compact />
          ))}
        </div>
      </div>
    </div>
  );
}

export function TutorialTaskModal({
  task,
  desktopReady,
  onClose,
  onBack,
  onApplyPrompt,
  onOpenDesktopConnect,
}) {
  const [downloadStarted, setDownloadStarted] = useState(false);
  if (!task) return null;
  const needsDesktop = task.requiresDesktop && !desktopReady;

  return (
    <div className="oc-modal-overlay" onClick={onClose}>
      <div className="oc-modal catsco-download-modal cc-tutorial-modal" onClick={(event) => event.stopPropagation()}>
        <div className="oc-modal-header catsco-download-header">
          <div>
            <h3>{task.title}</h3>
            <p>{task.detail}</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭">
            <X size={18} />
          </button>
        </div>

        <div className="cc-tutorial-detail">
          <div className="catsco-download-card">
            <span className="catsco-download-icon">
              <FileImage size={20} />
            </span>
            <span className="catsco-download-copy">
              <span className="catsco-download-title">{task.mediaName}</span>
              <span className="catsco-download-desc">示例图片会下载到浏览器默认下载文件夹。</span>
              {downloadStarted && (
                <span className="catsco-download-meta">已开始下载。下载完成后点击“填入任务”。</span>
              )}
            </span>
            <a
              className="catsco-download-action"
              href={task.mediaUrl}
              download={task.mediaName}
              onClick={() => setDownloadStarted(true)}
              title="下载示例图片"
            >
              <Download size={16} />
            </a>
          </div>

          <div className="cc-tutorial-requirement">
            需要：CatsCo 桌面端{desktopReady ? '已准备好' : '未连接时请先开启'}
          </div>

          <div className="cc-tutorial-prompt">
            <div className="cc-tutorial-prompt-title">将填入的任务说明</div>
            <div className="cc-tutorial-prompt-body">{task.prompt}</div>
          </div>

          <div className="cc-tutorial-actions">
            <button type="button" className="oc-btn oc-btn-default" onClick={onBack}>
              继续试另一个任务
            </button>
            <button
              type="button"
              className="oc-btn oc-btn-primary"
              onClick={() => {
                if (needsDesktop) {
                  onOpenDesktopConnect?.();
                  return;
                }
                onApplyPrompt(task.prompt);
              }}
            >
              {needsDesktop ? '先连接桌面端' : '填入任务'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function TutorialTaskCard({ task, onClick, compact = false }) {
  const Icon = task.icon;
  return (
    <button type="button" className={`cc-tutorial-card${compact ? ' compact' : ''}`} onClick={onClick}>
      <span className="cc-tutorial-card-icon">
        <Icon size={18} />
      </span>
      <span className="cc-tutorial-card-copy">
        <span className="cc-tutorial-card-title">{task.title}</span>
        <span className="cc-tutorial-card-desc">{task.description}</span>
      </span>
      <span className="cc-tutorial-card-arrow">
        <ChevronRight size={16} />
      </span>
    </button>
  );
}
