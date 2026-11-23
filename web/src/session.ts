import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError } from './api';

export type Session = { token: string; expiresAt: number; username: string; role: string };
const KEY = 'exchange.session';

function persist(session: Session | null): void {
  try {
    if (session) localStorage.setItem(KEY, JSON.stringify(session));
    else localStorage.removeItem(KEY);
  } catch {
    // Authentication is still usable for the current page if storage is blocked.
  }
}

function expired(session: Session): boolean {
  return session.expiresAt * 1000 <= Date.now();
}

function loadSession(): Session | null {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return null;
    const session: Partial<Session> | null = JSON.parse(raw);
    if (session && typeof session.token === 'string' && session.token &&
        typeof session.expiresAt === 'number' && Number.isFinite(session.expiresAt) &&
        typeof session.username === 'string' && typeof session.role === 'string' &&
        session.expiresAt * 1000 > Date.now()) {
      return { token: session.token, expiresAt: session.expiresAt, username: session.username, role: session.role };
    }
  } catch {
    // Treat a corrupt or inaccessible session as signed out.
  }
  persist(null);
  return null;
}

function superseded(): DOMException {
  return new DOMException('会话已结束', 'AbortError');
}

export function useSession() {
  const [session, setSession] = useState(loadSession);
  const [expiryNotice, setExpiryNotice] = useState(false);
  const owner = useRef(session);

  const signIn = useCallback((next: Session) => {
    owner.current = next;
    persist(next);
    setSession(next);
    setExpiryNotice(false);
  }, []);

  const end = useCallback((expected: Session, isExpired: boolean) => {
    if (owner.current !== expected) return;
    owner.current = null;
    persist(null);
    setSession(null);
    setExpiryNotice(isExpired);
  }, []);

  const signOut = useCallback(() => {
    if (owner.current) end(owner.current, false);
  }, [end]);

  useEffect(() => {
    if (!session) return;
    let timer: number;
    function check() {
      window.clearTimeout(timer);
      if (owner.current !== session) return;
      const remaining = session!.expiresAt * 1000 - Date.now();
      if (remaining <= 0) end(session!, true);
      else timer = window.setTimeout(check, Math.min(remaining, 2_147_483_647));
    }
    check();
    document.addEventListener('visibilitychange', check);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener('visibilitychange', check);
    };
  }, [session, end]);

  const runPrivate = useCallback(async <T,>(expected: Session, operation: (token: string) => Promise<T>): Promise<T> => {
    if (owner.current !== expected) throw superseded();
    if (expired(expected)) {
      end(expected, true);
      throw superseded();
    }
    try {
      const result = await operation(expected.token);
      if (owner.current !== expected) throw superseded();
      if (expired(expected)) {
        end(expected, true);
        throw superseded();
      }
      return result;
    } catch (error) {
      if (owner.current !== expected) throw superseded();
      if (error instanceof ApiError && error.status === 401) end(expected, true);
      throw error;
    }
  }, [end]);

  return { session, expiryNotice, signIn, signOut, runPrivate };
}
