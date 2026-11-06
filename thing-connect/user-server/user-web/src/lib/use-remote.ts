import { useEffect, useState } from 'react';
import { request } from './api';

// Explicit reads only. URL changes cancel obsolete reads; no focus, reconnect,
// background timer or automatic retry can replace a user's draft.
export function useRemote<T>(url: string | undefined) {
  const [data, setData] = useState<T>(), [error, setError] = useState<unknown>(), [loading, setLoading] = useState(!!url), [revision, setRevision] = useState(0);
  useEffect(() => {
    setData(undefined); setError(undefined); setLoading(!!url);
    if (!url) return;
    const controller = new AbortController();
    void request<T>(url, { signal: controller.signal }).then(value => { if (!controller.signal.aborted) setData(value); }).catch(e => { if (!controller.signal.aborted) setError(e); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [url, revision]);
  return { data, error, loading, reload: () => setRevision(value => value + 1) };
}
