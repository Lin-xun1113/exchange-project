import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

type Snapshot<T> = { scope: object; data?: T; error: Error | null };

export function usePoll<T>(
  fn: ((signal: AbortSignal) => Promise<T>) | null,
  intervalMs: number,
  deps: readonly unknown[],
) {
  // A changed dependency hides old data during render, before the old effect is cleaned up.
  const scope = useMemo(() => ({}), [intervalMs, fn !== null, ...deps]);
  const [snapshot, setSnapshot] = useState<Snapshot<T>>();
  const reschedule = useRef<(() => void) | null>(null);
  const refresh = useCallback(() => reschedule.current?.(), []);

  useEffect(() => {
    if (!fn) return;
    const load = fn;
    let active: AbortController | undefined;
    let stopped = false;

    async function run(replace = false) {
      if (stopped || document.hidden || (active && !replace)) return;
      active?.abort();
      const request = new AbortController();
      active = request;
      try {
        const data = await load(request.signal);
        if (!request.signal.aborted) setSnapshot({ scope, data, error: null });
      } catch (error) {
        if (!request.signal.aborted) {
          const failure = error instanceof Error ? error : new Error(String(error));
          setSnapshot((previous) => ({ scope, data: previous?.scope === scope ? previous.data : undefined, error: failure }));
        }
      } finally {
        if (active === request) active = undefined;
      }
    }

    const tick = () => void run();
    reschedule.current = () => void run(true);
    tick();
    const timer = window.setInterval(tick, intervalMs);
    document.addEventListener('visibilitychange', tick);
    return () => {
      stopped = true;
      active?.abort();
      reschedule.current = null;
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', tick);
    };
  }, [scope]);

  const current = snapshot?.scope === scope ? snapshot : undefined;
  return { data: current?.data, error: current?.error ?? null, refresh };
}
