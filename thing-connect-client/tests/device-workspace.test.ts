import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
function harness() {
  const state: any[] = [], refs: any[] = [], reads: boolean[] = [];
  let cursor = 0, refCursor = 0, mounted = false;
  let load = (_options: object) => {}, cleanup = () => {}, fail = false, owned = true, count = 0;
  class ApiError extends Error { code: number; constructor(code: number, message: string) { super(message); this.code = code; } }
  const source = loadSource(fileURLToPath(new URL('../src/hooks/device-workspace.ts', import.meta.url)), {
    react: {
      useState(initial: unknown) { const i = cursor++; if (!mounted) state[i] = initial; return [state[i], (value: unknown) => { state[i] = value; }]; },
      useRef(initial: unknown) { const i = refCursor++; if (!mounted) refs[i] = { current: initial }; return refs[i]; },
      useEffect(fn: () => () => void) { if (!mounted) cleanup = fn(); },
    },
    '@tarojs/taro': { useLoad(fn: typeof load) { load = fn; } },
    '../services/api': { ApiError, errorText: (e: Error) => e.message },
    '../services/device-directory': { readDevices: async (fresh: boolean) => { reads.push(fresh); return owned ? [{ device_id: 'device', status: 1 }] : []; } },
  });
  const read = async () => { count++; if (fail) throw new ApiError(40300, '设备不属于当前用户'); return 'business-data'; };
  function render() { cursor = 0; refCursor = 0; const result = source.useDeviceWorkspace(read); mounted = true; return result; }
  render();
  return { render, reads, start: () => load({ device_id: 'device' }), deny: () => { fail = true; }, unbind: () => { owned = false; }, count: () => count, unmount: () => cleanup() };
}
test('功能页首次使用设备快照，显式刷新读取最新设备关系', async () => {
  const h = harness(); h.start(); await flush();
  assert.deepEqual(h.reads, [false]); assert.equal(h.render().value, 'business-data');
  h.unbind(); await h.render().refresh();
  assert.deepEqual(h.reads, [false, true]); assert.equal(h.count(), 1);
  assert.equal(h.render().device, undefined); assert.equal(h.render().value, undefined);
  assert.match(h.render().error, /不属于/);
});
test('缓存存在也执行服务端业务读取，拒绝访问时显示错误', async () => {
  const h = harness(); h.deny(); h.start(); await flush();
  assert.equal(h.count(), 1); assert.equal(h.render().value, undefined);
  assert.match(h.render().error, /设备不属于当前用户/);
  assert.equal(h.render().busy, false);
});
test('卸载后的设备快照读取不继续启动业务请求', async () => {
  const h = harness(); h.start(); h.unmount(); await flush();
  assert.equal(h.count(), 0);
});
