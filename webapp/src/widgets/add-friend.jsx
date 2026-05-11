import React, { useState } from 'react';
import { api } from '../api';
import t from '../i18n';
import Avatar from './avatar';

export default function AddFriend({ currentUser, onClose, onSent }) {
  const [query, setQuery] = useState('');
  const [message, setMessage] = useState(() => defaultFriendMessage(currentUser));
  const [results, setResults] = useState([]);
  const [sent, setSent] = useState(new Set());
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const handleSearch = async () => {
    if (query.trim().length < 2) {
      setError(t('friend_search_too_short'));
      return;
    }

    setLoading(true);
    setError('');
    try {
      const res = await api.searchUsers(query.trim());
      setResults(res.users || []);
    } catch (e) {
      console.error('search:', e);
      setError(e.message || t('error_server'));
    } finally {
      setLoading(false);
    }
  };

  const handleSend = async (userId) => {
    setError('');
    try {
      await api.sendFriendRequest(userId, message.trim());
      setSent((prev) => new Set([...prev, userId]));
      if (onSent) onSent();
    } catch (e) {
      console.error('send request:', e);
      setError(e.message || t('error_server'));
    }
  };

  return (
    <div className="oc-modal-overlay" onClick={onClose}>
      <div className="oc-modal" onClick={(e) => e.stopPropagation()}>
        <div className="oc-modal-title">{t('contacts_add_friend')}</div>
        <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
          <input
            className="oc-auth-input"
            style={{ marginBottom: 0, flex: 1 }}
            placeholder={t('contacts_search_placeholder')}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
          />
          <button className="oc-btn oc-btn-primary" onClick={handleSearch}>
            {loading ? t('loading') : t('search')}
          </button>
        </div>
        <input
          className="oc-auth-input"
          placeholder={t('friend_request_message')}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
        />

        {error && <div className="oc-form-error">{error}</div>}

        {results.map((user) => (
          <div key={user.id} className="oc-contact-item">
            <Avatar
              name={user.display_name || user.username}
              src={user.avatar_url}
              size={40}
              isBot={user.account_type === 'bot'}
              className="oc-contact-avatar"
            />
            <span className="oc-contact-name" style={{ flex: 1 }}>
              {user.display_name || user.username}
            </span>
            {sent.has(user.id) ? (
              <span style={{ color: '#888', fontSize: 13 }}>
                {t('friend_request_sent')}
              </span>
            ) : (
              <button
                className="oc-btn oc-btn-primary"
                onClick={() => handleSend(user.id)}
              >
                {t('friend_request_send')}
              </button>
            )}
          </div>
        ))}

        {results.length === 0 && query.length >= 2 && (
          <div style={{ textAlign: 'center', color: '#888', padding: 20 }}>
            {t('no_data')}
          </div>
        )}
      </div>
    </div>
  );
}

function defaultFriendMessage(user) {
  const name = user?.display_name || user?.username || '';
  return name ? t('friend_request_default_msg', { name }) : '';
}
