import { sessionToken } from './api';

// Presentation snapshots are tab-local and account-scoped. They are restored
// only on back navigation, never as a replacement for an explicit refresh.
export async function snapshotKey(page: string) {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(sessionToken() ?? ''));
  return 'device-list-v1:react:' + [...new Uint8Array(digest)].map(byte => byte.toString(16).padStart(2, '0')).join('') + ':' + page;
}
export function restoreSnapshot<T>(key: string): T | undefined {
  try {
    const nav = performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming | undefined;
    if (nav?.type === 'reload') return;
    if (sessionStorage.getItem(key + ':away') !== '1' && nav?.type !== 'back_forward') return;
    const saved = sessionStorage.getItem(key);
    sessionStorage.removeItem(key + ':away');
    if (saved) return JSON.parse(saved) as T;
  } catch { /* A tab with storage disabled can still use explicit requests. */ }
}
export function saveSnapshot<T>(key: string, value: T) {
  try { sessionStorage.setItem(key, JSON.stringify(value)); } catch { /* Optional presentation cache. */ }
}
export function markAway(key: string) {
  try { sessionStorage.setItem(key + ':away', '1'); } catch { /* Optional presentation cache. */ }
}
