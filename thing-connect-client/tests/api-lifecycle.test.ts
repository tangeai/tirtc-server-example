import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

function harness() {
  let session = 'account-A';
  const calls: any[] = [], redirects: any[] = [], pending: { resolve(value: unknown): void; reject(error: unknown): void }[] = [];
  const taro = { getStorageSync: () => session, setStorageSync: (_: string, value: string) => { session = value; }, removeStorageSync: () => { session = ''; },
    request: (options: unknown) => { calls.push(options); return new Promise((resolve, reject) => pending.push({ resolve, reject })); },
    reLaunch: (options: unknown) => { redirects.push(options); return new Promise(() => {}); } };
  const api = loadSource(fileURLToPath(new URL('../src/services/api.ts', import.meta.url)), { '@tarojs/taro': { __esModule: true, default: taro } }, { API_ORIGIN: 'https://fixture.invalid' });
  return { api, calls, redirects, pending, session: () => session };
}
test('公共接口不发送 JWT，业务接口发送当前 JWT，所有请求有超时', async () => {
  const h = harness(); const publicRequest = h.api.request('/v1/config/captcha');
  assert.equal(h.calls[0].header.Authorization, undefined); assert.equal(h.calls[0].timeout, 15000);
  h.pending[0].resolve({ statusCode: 200, data: { code: 200, data: { enabled: false } } }); await publicRequest;
  const protectedRequest = h.api.request('/v1/user/me');
  assert.equal(h.calls[1].header.Authorization, 'Bearer account-A');
  h.pending[1].resolve({ statusCode: 200, data: { code: 200, data: {} } }); await protectedRequest;
});
test('旧账号响应不能回写新账号；旧 401 不能踢出新账号', async () => {
  const h = harness(); const staleSuccess = h.api.request('/v1/user/me'); const stale401 = h.api.request('/v1/user/device/list');
  h.api.signIn('account-B');
  h.pending[0].resolve({ statusCode: 200, data: { code: 200, data: { email: 'old@example.invalid' } } });
  await assert.rejects(staleSuccess, (e: any) => e.code === -2);
  h.pending[1].resolve({ statusCode: 401, data: { code: 401, msg: '登录已过期' } });
  await assert.rejects(stale401); assert.equal(h.session(), 'account-B'); assert.equal(h.redirects.length, 0);
});
test('并发 JWT 过期只跳转一次，业务权限拒绝不清除登录', async () => {
  const h = harness(); const denied = h.api.request('/v1/user/me');
  h.pending[0].resolve({ statusCode: 403, data: { code: 40300, msg: '无权访问' } });
  await assert.rejects(denied); assert.equal(h.session(), 'account-A');
  const first = h.api.request('/v1/user/me'), second = h.api.request('/v1/user/device/list');
  for (const p of h.pending.slice(1)) p.resolve({ statusCode: 401, data: { code: 401, msg: '登录已过期' } });
  await Promise.all([assert.rejects(first), assert.rejects(second)]);
  assert.equal(h.redirects.length, 1); assert.equal(h.session(), '');
});
test('HTTP 200 的业务冲突保持失败，且不会自动重试写入', async () => {
  const h = harness(); const result = h.api.request('/v1/call/group/web/device/test/join', 'POST', { room_code: '001234' });
  h.pending[0].resolve({ statusCode: 200, data: { code: 40923, msg: '已在其他房间' } });
  await assert.rejects(result, (e: any) => e.code === 40923); assert.equal(h.calls.length, 1);
});
