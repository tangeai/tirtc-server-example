import Taro from '@tarojs/taro';
import { ApiError, decodeResponse } from '../domain/http';
export { ApiError, errorText } from '../domain/http';

const publicPaths = new Set(['/v1/config/captcha', '/v1/config/navigation', '/v1/user/login', '/v1/user/register', '/v1/user/send-code', '/v1/user/password-reset', '/v1/user/password-reset/send-code']);
let redirecting = false;
export function token(): string { return Taro.getStorageSync('token') || ''; }
export function signIn(value: string) { Taro.setStorageSync('token', value); }
export function signOut() { Taro.removeStorageSync('token'); }
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
      void Taro.reLaunch({ url: '/pages/login/index?expired=1' }).finally(() => { redirecting = false; });
    }
    throw error;
  }
}
