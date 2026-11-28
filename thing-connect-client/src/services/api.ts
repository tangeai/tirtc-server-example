import { requireBrowserLogin } from '../platform/console-session';
import { browserToken } from '../platform/browser-token';
import Taro from '@tarojs/taro';
import { ApiError, decodeResponse } from '../domain/http';
export { ApiError, errorText } from '../domain/http';

const publicPaths = new Set(['/v1/config/captcha', '/v1/config/navigation', '/v1/user/login', '/v1/user/register', '/v1/user/send-code', '/v1/user/password-reset', '/v1/user/password-reset/send-code']);
let redirecting = false;
export function token(): string { return (process.env.TARO_ENV === 'h5' ? browserToken() : Taro.getStorageSync('token')) || ''; }
const rememberedEmailKey = 'last-successful-login-email';
export function rememberedEmail(): string {
  try {
    const value = Taro.getStorageSync(rememberedEmailKey);
    return typeof value === 'string' && value.length <= 254 ? value : '';
  } catch { return ''; }
}
export function signIn(value: string, email?: string) {
  if (process.env.TARO_ENV === 'h5') localStorage.setItem('token', value);
  else Taro.setStorageSync('token', value);
  if (email?.trim()) {
    try { Taro.setStorageSync(rememberedEmailKey, email.trim()); } catch { /* Remembering an email must not prevent login. */ }
  }
}
export function signOut() { if (process.env.TARO_ENV === 'h5') localStorage.removeItem('token'); else Taro.removeStorageSync('token'); }
export async function request<T>(path: string, method: 'GET' | 'POST' | 'PUT' | 'DELETE' = 'GET', data?: object, headers?: Record<string, string>): Promise<T> {
  const requestToken = token();
  const isPublic = publicPaths.has(path.split('?')[0]);
  try {
    const response = await Taro.request({
      url: API_ORIGIN.replace(/\/$/, '') + path, method, data, timeout: 15000,
      header: { ...headers, 'content-type': 'application/json', ...(!isPublic && requestToken ? { Authorization: `Bearer ${requestToken}` } : {}) },
    });
    const result = decodeResponse<T>(response.statusCode, response.data, path.startsWith('/v1/voip/') ? 0 : 200);
    if (!isPublic && requestToken !== token()) throw new ApiError(-2, '账号已切换，请重新打开页面');
    return result;
  } catch (error) {
    if (!isPublic && error instanceof ApiError && error.code === 401 && token() === requestToken && !redirecting) {
      redirecting = true; signOut();
      if (process.env.TARO_ENV === 'h5') { requireBrowserLogin(true); throw error; }
      const notification = /^\/v1\/push\/user\/references\/([0-9a-f-]{36})$/.exec(path)?.[1];
      void Taro.reLaunch({ url: '/pages/login/index?expired=1' + (notification ? '&notification=' + notification : '') }).finally(() => { redirecting = false; });
    }
    throw error;
  }
}
