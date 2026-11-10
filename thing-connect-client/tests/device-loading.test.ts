import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
function nodes(value: any): any[] {
  if (!value || typeof value !== 'object') return [];
  if (Array.isArray(value)) return value.flatMap(nodes);
  return [value, ...nodes(value.props?.children)];
}
function harness() {
  const state: any[] = [], refs: any[] = [], cleanups: (() => void)[] = [];
  let cursor = 0, refCursor = 0, mounted = false, show = () => {}, pull = () => {}, stopped = 0;
  const authorizations: { resolve(value: unknown): void; reject(error: unknown): void }[] = [];
  const jsx = (type: unknown, props: any) => ({ type, props });
  const device = { device_id: 'device', device_name: '测试', online: true, status: 1 };
  const intents: string[] = [];
  const source = loadSource(fileURLToPath(new URL('../src/pages/devices/index.tsx', import.meta.url)), {
    'react/jsx-runtime': { jsx, jsxs: jsx },
    react: {
      useState(initial: unknown) { const i = cursor++; if (!mounted) state[i] = initial; return [state[i], (value: any) => { state[i] = typeof value === 'function' ? value(state[i]) : value; }]; },
      useRef(initial: unknown) { const i = refCursor++; if (!mounted) refs[i] = { current: initial }; return refs[i]; },
      useEffect(fn: () => void | (() => void)) { if (!mounted) { const cleanup = fn(); if (cleanup) cleanups.push(cleanup); } },
    },
    '@tarojs/components': { View: 'view', Text: 'text', Button: 'button' },
    '@tarojs/taro': { __esModule: true, default: { stopPullDownRefresh() { stopped++; } }, useDidShow(fn: typeof show) { show = fn; }, usePullDownRefresh(fn: typeof pull) { pull = fn; } },
    '../../components/ui': { Action: 'action', Field: 'field', Notice: 'notice', Sheet: 'sheet' },
    '../../services/api': { token: () => 'account', errorText: () => '读取失败' },
    '../../services/device-events': { deviceRevision: () => 0 },
    '../../services/device-directory': { readDevices: async () => [device] },
    '../../platform/media': {
      mediaCapabilities: () => ({ platform: 'weapp', playback: true }), cacheDeviceMedia() {},
      refreshAuthorizations: () => new Promise((resolve, reject) => authorizations.push({ resolve, reject })),
      performMedia: async (intent: string) => { intents.push(intent); },
    },
  });
  function render() { cursor = 0; refCursor = 0; const tree = source.default(); mounted = true; return tree; }
  function callButton() {
    const group = nodes(render()).find(n => n.props?.className === 'device-actions device-primary-actions');
    return nodes(group).find(n => n.type === 'action');
  }
  render();
  return { render, callButton, show: () => show(), pull: () => pull(), stopped: () => stopped, authorizations, intents, unmount: () => cleanups.forEach(fn => fn()) };
}
test('慢授权不阻塞列表和下拉结束，未知授权仍经媒体模块确认', async () => {
  const h = harness(); h.show(); await flush();
  assert.equal(h.stopped(), 1);
  const button = h.callButton(); assert.equal(button.props.disabled, false);
  assert.ok(nodes(button).some(n => n.props?.children?.includes?.('确认通话授权')));
  button.props.onClick(); await flush();
  assert.deepEqual(h.intents, ['wechat-call']);
});
test('授权刷新失败保持未知，不显示为已撤销', async () => {
  const h = harness(); h.show(); await flush();
  h.authorizations[0].reject(new Error('offline')); await flush();
  assert.ok(nodes(h.callButton()).some(n => n.props?.children?.includes?.('确认通话授权')));
});
test('旧授权刷新不能覆盖手动授权后的结果', async () => {
  const h = harness(); h.show(); await flush();
  h.authorizations[0].resolve({ device: { state: 'missing', serverSynced: false } }); await flush();
  const authorize = h.callButton().props.onClick;
  h.pull(); await flush(); // Slow background read, started before explicit authorization.
  authorize(); await flush();
  assert.deepEqual(h.intents, ['authorize']);
  h.authorizations[2].resolve({ device: { state: 'authorized', serverSynced: true } }); await flush();
  h.authorizations[1].resolve({ device: { state: 'missing', serverSynced: false } }); await flush();
  assert.ok(nodes(h.callButton()).some(n => n.props?.children?.includes?.('微信通话')));
});
test('卸载后迟到的授权结果不回写', async () => {
  const h = harness(); h.show(); await flush(); h.unmount();
  h.authorizations[0].resolve({ device: { state: 'missing', serverSynced: false } }); await flush();
  assert.ok(nodes(h.callButton()).some(n => n.props?.children?.includes?.('确认通话授权')));
});
