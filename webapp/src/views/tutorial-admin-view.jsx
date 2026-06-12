import React, { useEffect, useState } from 'react';
import { api } from '../api';
import { TUTORIAL_TASKS } from '../widgets/tutorial-tasks';

const USER = 'catsco_tutorial_admin';
const PASS = 'catsco_tutorial_2026';
const TOKEN = `${USER}:${PASS}`;

export default function TutorialAdminView() {
  const [loggedIn, setLoggedIn] = useState(false);
  const [form, setForm] = useState({ user: '', pass: '' });
  const [tasks, setTasks] = useState(TUTORIAL_TASKS);
  const [limit, setLimit] = useState(6);
  const [status, setStatus] = useState('');

  useEffect(() => {
    if (!loggedIn) return;
    api.getTutorialTasks()
      .then((data) => {
        if (Array.isArray(data.tasks) && data.tasks.length > 0) setTasks(data.tasks);
        if (data.limit) setLimit(data.limit);
      })
      .catch(() => setStatus('读取云端配置失败，当前显示默认任务'));
  }, [loggedIn]);

  const updateTask = (index, patch) => {
    setTasks((prev) => prev.map((task, i) => (i === index ? { ...task, ...patch } : task)));
  };

  const upload = async (index, file) => {
    if (!file) return;
    setStatus('上传中...');
    try {
      const result = await api.uploadTutorialFile(file, TOKEN);
      updateTask(index, { mediaName: result.name || file.name, mediaUrl: result.url });
      setStatus('上传完成');
    } catch (error) {
      setStatus(error.message || '上传失败');
    }
  };

  const save = async () => {
    setStatus('保存中...');
    try {
      const data = await api.saveTutorialTasks({ limit: Number(limit) || 6, tasks }, TOKEN);
      setTasks(Array.isArray(data.tasks) ? data.tasks : tasks);
      setLimit(data.limit || limit);
      setStatus('已保存到云端');
    } catch (error) {
      setStatus(error.message || '保存失败');
    }
  };

  if (!loggedIn) {
    return (
      <div style={pageStyle}>
        <form style={cardStyle} onSubmit={(event) => {
          event.preventDefault();
          if (form.user === USER && form.pass === PASS) setLoggedIn(true);
          else setStatus('账号或密码错误');
        }}>
          <h2>示例任务管理</h2>
          <input style={inputStyle} placeholder="账号" value={form.user} onChange={(e) => setForm({ ...form, user: e.target.value })} />
          <input style={inputStyle} placeholder="密码" type="password" value={form.pass} onChange={(e) => setForm({ ...form, pass: e.target.value })} />
          <button style={buttonStyle}>进入</button>
          <p style={mutedStyle}>开发账号：{USER} / {PASS}</p>
          {status && <p>{status}</p>}
        </form>
      </div>
    );
  }

  return (
    <div style={pageStyle}>
      <div style={{ ...cardStyle, maxWidth: 980 }}>
        <h2>示例任务管理</h2>
        <label>展示上限 <input style={{ ...inputStyle, width: 80 }} type="number" min="1" max="12" value={limit} onChange={(e) => setLimit(e.target.value)} /></label>
        <button style={buttonStyle} onClick={() => setTasks([...tasks, emptyTask()])}>添加任务</button>
        <button style={buttonStyle} onClick={save}>保存到云端</button>
        {status && <span style={{ marginLeft: 12 }}>{status}</span>}
        <div style={{ display: 'grid', gap: 16, marginTop: 20 }}>
          {tasks.map((task, index) => (
            <div key={task.id || index} style={taskStyle}>
              <input style={inputStyle} placeholder="标题" value={task.title || ''} onChange={(e) => updateTask(index, { title: e.target.value })} />
              <input style={inputStyle} placeholder="短描述" value={task.description || ''} onChange={(e) => updateTask(index, { description: e.target.value })} />
              <textarea style={inputStyle} rows={2} placeholder="说明" value={task.detail || ''} onChange={(e) => updateTask(index, { detail: e.target.value })} />
              <textarea style={inputStyle} rows={4} placeholder="Prompt" value={task.prompt || ''} onChange={(e) => updateTask(index, { prompt: e.target.value })} />
              <input style={inputStyle} placeholder="附件文件名" value={task.mediaName || ''} onChange={(e) => updateTask(index, { mediaName: e.target.value })} />
              <input style={inputStyle} placeholder="附件 URL" value={task.mediaUrl || ''} onChange={(e) => updateTask(index, { mediaUrl: e.target.value })} />
              <input type="file" onChange={(e) => upload(index, e.target.files?.[0])} />
              <label><input type="checkbox" checked={task.requiresDesktop !== false} onChange={(e) => updateTask(index, { requiresDesktop: e.target.checked })} /> 需要桌面端</label>
              <select style={inputStyle} value={task.iconType || 'image'} onChange={(e) => updateTask(index, { iconType: e.target.value })}>
                <option value="image">读图/媒体</option>
                <option value="move">移动文件</option>
              </select>
              <button style={dangerStyle} onClick={() => setTasks(tasks.filter((_, i) => i !== index))}>删除</button>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

function emptyTask() {
  return { id: `task-${Date.now()}`, title: '', description: '', detail: '', mediaName: '', mediaUrl: '', requiresDesktop: true, iconType: 'image', prompt: '' };
}

const pageStyle = { minHeight: '100vh', padding: 32, background: '#111217', color: '#f5f7fb' };
const cardStyle = { maxWidth: 420, margin: '8vh auto', padding: 24, background: '#1b1b22', border: '1px solid #33343d', borderRadius: 8 };
const taskStyle = { display: 'grid', gap: 8, padding: 14, border: '1px solid #33343d', borderRadius: 8 };
const inputStyle = { width: '100%', boxSizing: 'border-box', padding: 10, borderRadius: 6, border: '1px solid #444650', background: '#12131a', color: '#f5f7fb' };
const buttonStyle = { marginRight: 8, padding: '10px 14px', borderRadius: 6, border: 0, background: '#10b981', color: '#08130f', fontWeight: 700 };
const dangerStyle = { ...buttonStyle, background: '#ef4444', color: '#fff' };
const mutedStyle = { color: '#9ca3af', fontSize: 13 };
