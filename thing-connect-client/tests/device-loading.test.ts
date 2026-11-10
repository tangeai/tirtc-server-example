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
function harness(deviceName = '测试') {
  const state: any[] = [], refs: any[] = [], cleanups: (() => void)[] = [];
  let cursor = 0, refCursor = 0, mounted = false, show = () => {}, pull = () => {}, stopped = 0;
  const authorizations: { resolve(value: unknown): void; reject(error: unknown): void }[] = [];
  const jsx = (type: unknown, props: any) => ({ type, props });
  const device = { device_id: 'device', device_name: deviceName, online: true, status: 1 };
  const intents: string[] = [];
  const writes: { path: string; method: string; body: any }[] = [];
  let resolveWrite = () => {}, changed = 0;
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
    '../../services/api': { token: () => 'account', errorText: () => '读取失败', request: (path: string, method: string, body: any) => { writes.push({ path, method, body }); return new Promise<void>(resolve => { resolveWrite = resolve; }); } },
    '../../services/device-events': { deviceRevision: () => changed, devicesChanged: () => { changed++; } },
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
  return { render, callButton, writes, resolveWrite: () => resolveWrite(), changed: () => changed, show: () => show(), pull: () => pull(), stopped: () => stopped, authorizations, intents, unmount: () => cleanups.forEach(fn => fn()) };
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

for (const name of ['', '   ']) {
  test('空名称提供直接命名入口：' + JSON.stringify(name), async () => {
    const h = harness(name); h.show(); await flush();
    const button = nodes(h.render()).find(n => n.props?.className === 'device-name-setup');
    assert.ok(button); assert.equal(button.props.disabled, false);
    assert.match(button.props.ariaLabel, /设置设备名称/);
    button.props.onClick();
    let tree = h.render();
    assert.ok(nodes(tree).some(n => n.type === 'sheet' && n.props.title === '修改设备名称'));
    const field = () => nodes(h.render()).find(n => n.type === 'field' && n.props.label === '设备名称');
    const save = () => nodes(h.render()).find(n => n.type === 'action' && n.props.children === '保存名称').props.onClick();
    field().props.onChange('   '); save(); await flush();
    assert.equal(h.writes.length, 0);
    field().props.onChange('名'.repeat(14)); save(); await flush();
    assert.equal(h.writes.length, 0);
    assert.ok(nodes(h.render()).some(n => n.props?.children === '请输入 1–13 个字符的设备名称'));
    const valid = '名'.repeat(12) + '😀';
    field().props.onChange(' ' + valid + ' '); save(); await flush();
    assert.equal(h.writes.length, 1);
    assert.equal(h.writes[0].path, '/v1/user/device/name');
    assert.equal(h.writes[0].method, 'PUT');
    assert.equal(h.writes[0].body.device_name, valid);
    assert.equal(nodes(h.render()).find(n => n.props?.className === 'device-name-setup').props.disabled, true);
    h.resolveWrite(); await flush(); tree = h.render();
    assert.ok(nodes(tree).some(n => n.props?.className === 'device-name' && n.props.children === valid));
    assert.equal(nodes(tree).some(n => n.props?.className === 'device-name-setup'), false);
    assert.equal(nodes(tree).some(n => n.type === 'sheet'), false);
    assert.equal(h.changed(), 1);
  });
}
test('已有名称保持普通标题及更多菜单修改入口', async () => {
  const h = harness(); h.show(); await flush();
  assert.equal(nodes(h.render()).some(n => n.props?.className === 'device-name-setup'), false);
  nodes(h.render()).find(n => n.type === 'action' && n.props.children === '⋯').props.onClick();
  nodes(h.render()).find(n => n.props?.title === '修改设备名称').props.onClick();
  assert.equal(nodes(h.render()).find(n => n.type === 'field' && n.props.label === '设备名称').props.value, '测试');
});
