import { useLayoutEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { login, register } from './api';
import { Modal } from './Modal';
import type { Session } from './session';
import { Tabs } from './Tabs';

const modes = [{ value: 'login', label: '登录' }, { value: 'register', label: '注册' }] as const;
type Mode = (typeof modes)[number]['value'];
type Props = { onClose: () => void; onAuthenticated: (session: Session) => void };

export function AuthDialog({ onClose, onAuthenticated }: Props) {
  const [mode, setMode] = useState<Mode>('login');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const attempt = useRef<AbortController>(null);
  const registering = mode === 'register';

  useLayoutEffect(() => () => attempt.current?.abort(), []);

  function changeMode(next: Mode) {
    if (next === mode) return;
    attempt.current?.abort();
    attempt.current = null;
    setMode(next);
    setPending(false);
    setError('');
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (attempt.current) return;
    const fields = new FormData(event.currentTarget);
    const username = String(fields.get('username')).trim();
    const password = String(fields.get('password'));
    const email = String(fields.get('email') ?? '').trim();
    const controller = new AbortController();
    const { signal } = controller;
    attempt.current = controller;
    setPending(true);
    setError('');
    try {
      if (registering) {
        await register({ username, password, email: email || undefined }, signal);
        if (signal.aborted) return;
      }
      const result = await login({ username, password }, signal);
      if (!signal.aborted) onAuthenticated({
        token: result.token, expiresAt: result.expires_at, username: result.username, role: result.role,
      });
    } catch (failure) {
      if (!signal.aborted) setError(failure instanceof Error ? failure.message : '登录失败');
    } finally {
      if (attempt.current === controller && !signal.aborted) {
        attempt.current = null;
        setPending(false);
      }
    }
  }

  return (
    <Modal labelId="auth-heading" title="交易账户" onClose={() => { attempt.current?.abort(); onClose(); }}>
      <Tabs id="auth" label="账户验证" items={modes} active={mode} onChange={changeMode} className="auth-tabs" />
      <div id="auth-panel" role="tabpanel" aria-labelledby={`auth-${mode}`}>
        <form className="dialog-form" onSubmit={submit}>
          <label className="field">
            <span>用户名</span>
            <input name="username" autoComplete="username" required minLength={registering ? 3 : undefined} maxLength={32} data-dialog-focus />
          </label>
          <label className="field">
            <span>密码</span>
            <input key={mode} name="password" type="password" autoComplete={registering ? 'new-password' : 'current-password'}
              required minLength={registering ? 6 : undefined} />
          </label>
          {registering ? (
            <label className="field"><span>邮箱（选填）</span><input name="email" type="email" autoComplete="email" /></label>
          ) : <p className="dialog-note">演示账号 <span>demo / demo1234</span></p>}
          {error && <p className="form-notice fall" role="alert">{error}</p>}
          <button type="submit" className="button button-accent" disabled={pending}>
            {registering ? (pending ? '正在注册' : '注册并登录') : (pending ? '正在登录' : '登录')}
          </button>
        </form>
      </div>
    </Modal>
  );
}
