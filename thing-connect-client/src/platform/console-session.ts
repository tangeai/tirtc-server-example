import { browserToken } from './browser-token';

const protectedPages = new Set(['/devices', '/bind', '/contacts', '/agent', '/room', '/account', '/history', '/recognition', '/notifications']);
const returnKey = 'console.return-page';

export function requireBrowserLogin(expired = false) {
  if (protectedPages.has(location.pathname)) {
    try { sessionStorage.setItem(returnKey, location.pathname + location.search); } catch { /* Storage is optional. */ }
  }
  location.replace(expired ? '/login?expired=1' : '/login');
}
export function afterBrowserLogin() {
  let target = '';
  try { target = sessionStorage.getItem(returnKey) || ''; sessionStorage.removeItem(returnKey); } catch { /* Default to devices. */ }
  if (!target.startsWith('/') || target.startsWith('//')) return '/devices';
  const url = new URL(target || '/devices', location.origin);
  return url.origin === location.origin && protectedPages.has(url.pathname) ? url.pathname + url.search : '/devices';
}
export function guardBrowserPage() {
  if (protectedPages.has(location.pathname) && !browserToken()) requireBrowserLogin();
}
