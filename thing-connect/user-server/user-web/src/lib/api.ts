export interface Envelope<T> { code: number; msg?: string; data: T }
export class ApiError extends Error {
  readonly code: number;
  readonly data: unknown;
  constructor(code: number, message: string, data?: unknown) { super(message); this.code = code; this.data = data; }
}
declare global { interface Window { MiniProgramPage?: { embedded: boolean; token: string | null; requireLogin(): void; goBack(): void } } }
export const sessionToken = () => window.MiniProgramPage?.embedded ? window.MiniProgramPage.token : localStorage.getItem('token');
let expired = false;
export function clearSession() {
  localStorage.removeItem('token');
  clearDeviceSnapshots();
}
export function clearDeviceSnapshots() {
  for (const key of Object.keys(sessionStorage)) {
    if (key.startsWith('device-list-v1:')) sessionStorage.removeItem(key);
  }
}
function expireSession(): never {
  if (expired) throw new ApiError(401, '登录状态已失效，请重新登录');
  expired = true;
  if (window.MiniProgramPage?.embedded) {
    window.MiniProgramPage.requireLogin();
  } else {
    clearSession();
    sessionStorage.setItem('login-notice', 'expired');
    location.replace('/login');
  }
  throw new ApiError(401, '登录状态已失效，请重新登录');
}
export async function request<T>(path: string, options: {
  method?: string; headers?: Record<string, string>; body?: unknown; form?: FormData; signal?: AbortSignal; anonymous?: boolean;
} = {}): Promise<T> {
  const headers: Record<string, string> = { ...options.headers };
  if (!options.anonymous) headers.Authorization = 'Bearer ' + (sessionToken() ?? '');
  if (options.body !== undefined) headers['Content-Type'] = 'application/json';
  const signal = options.signal ? AbortSignal.any([options.signal, AbortSignal.timeout(options.form ? 60000 : 15000)]) : AbortSignal.timeout(options.form ? 60000 : 15000);
  const response = await fetch(path, { method: options.method ?? 'GET', headers, signal, cache: 'no-store',
    body: options.form ?? (options.body === undefined ? undefined : JSON.stringify(options.body)) });
  // A proxy may return an HTML or empty 401 response. Authentication expiry
  // must not depend on successful JSON parsing.
  if (!options.anonymous && response.status === 401) expireSession();
  const result = await response.json() as Envelope<T>;
  if (!options.anonymous && (response.status === 401 || result.code === 401)) {
    expireSession();
  }
  if (!response.ok || result.code !== 200) throw new ApiError(result.code, result.msg || '操作失败，请重试', result.data);
  return result.data;
}
export function errorText(error: unknown): string {
  if (error instanceof ApiError) return window.WebI18n?.errorMessage({ code: error.code, msg: error.message }) ?? error.message;
  if (error instanceof DOMException && ['AbortError', 'TimeoutError'].includes(error.name)) return '请求超时，请检查网络后重试';
  return '网络连接失败，请稍后重试';
}
