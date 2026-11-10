import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

function harness() {
  let session = 'account-A', revision = 0;
  const pending: { resolve(value: unknown): void; reject(error: unknown): void }[] = [];
  class ApiError extends Error { code: number; constructor(code: number, message: string) { super(message); this.code = code; } }
  const directory = loadSource(fileURLToPath(new URL('../src/services/device-directory.ts', import.meta.url)), {
    './api': { ApiError, token: () => session, request: () => new Promise((resolve, reject) => pending.push({ resolve, reject })) },
    './device-events': { deviceRevision: () => revision },
  });
  return { directory, pending, change: () => { revision++; }, account: (value: string) => { session = value; } };
}
const device = { device_id: 'device', status: 1, device_name: '原名' };
test('多个功能页共享快照与在途读取，手动刷新重新请求', async () => {
  const h = harness();
  const first = h.directory.readDevices(), second = h.directory.readDevices();
  assert.equal(h.pending.length, 1);
  h.pending[0].resolve([device, { ...device, status: 0 }]);
  assert.equal((await first).length, 1); await second;
  assert.equal((await h.directory.readDevices())[0].device_id, 'device');
  assert.equal(h.pending.length, 1);
  const refreshed = h.directory.readDevices(true);
  h.pending[1].resolve([{ ...device, device_name: '新名' }]);
  assert.equal((await refreshed)[0].device_name, '新名');
});
test('绑定、改名、解绑失效后不能被旧请求覆盖', async () => {
  const h = harness(); const old = h.directory.readDevices();
  h.change(); const latest = h.directory.readDevices();
  h.pending[1].resolve([]); await latest;
  h.pending[0].resolve([device]); await assert.rejects(old, (e: any) => e.code === -2);
  assert.equal((await h.directory.readDevices()).length, 0);
});
test('切换账号隔离缓存与在途请求，退出后不能读取快照', async () => {
  const h = harness(); const old = h.directory.readDevices();
  h.account('account-B'); const next = h.directory.readDevices();
  h.pending[0].resolve([device]); await assert.rejects(old);
  h.pending[1].resolve([{ ...device, device_id: 'B' }]); await next;
  assert.equal((await h.directory.readDevices())[0].device_id, 'B');
  h.account(''); await assert.rejects(h.directory.readDevices(), (e: any) => e.code === 401);
});
test('强制刷新隔离旧结果，读取失败允许再次重试', async () => {
  const h = harness(); const old = h.directory.readDevices(), fresh = h.directory.readDevices(true);
  h.pending[0].resolve([device]); await assert.rejects(old);
  h.pending[1].reject(new Error('offline')); await assert.rejects(fresh);
  const retry = h.directory.readDevices(); assert.equal(h.pending.length, 3);
  h.pending[2].resolve([device]); await retry;
});
