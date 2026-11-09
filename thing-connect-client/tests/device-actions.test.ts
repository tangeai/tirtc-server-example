import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

type Node = { type?: unknown; props?: any };
function nodes(value: any): Node[] {
  if (!value || typeof value !== 'object') return [];
  if (Array.isArray(value)) return value.flatMap(nodes);
  return [value, ...nodes(value.props?.children)];
}
function render(state: string, online: boolean, menu = false) {
  const device = { device_id: 'device/a', device_name: '测试设备', status: 1, online };
  const values = [[device], false, true, '', { [device.device_id]: { state, serverSynced: true } }, menu ? device : undefined];
  let cursor = 0;
  const navigation: string[] = [], intents: string[] = [], tabActions: string[] = [], toasts: string[] = [];
  const cleanups: (() => void)[] = [];
  const jsx = (type: unknown, props: any) => ({ type, props });
  const source = loadSource(fileURLToPath(new URL('../src/pages/devices/index.tsx', import.meta.url)), {
    'react/jsx-runtime': { jsx, jsxs: jsx },
    react: { useState: (initial: unknown) => [cursor < values.length ? values[cursor++] : (cursor++, initial), () => {}], useRef: (current: unknown) => ({ current }), useEffect: (fn: () => void | (() => void)) => { const cleanup = fn(); if (cleanup) cleanups.push(cleanup); } },
    '@tarojs/taro': { __esModule: true, default: { showToast: ({ title }: { title: string }) => { toasts.push(title); }, hideTabBar: async () => { tabActions.push('hide'); }, showTabBar: async () => { tabActions.push('show'); }, navigateTo: ({ url }: { url: string }) => { navigation.push(url); } }, useDidShow() {}, usePullDownRefresh() {} },
    '@tarojs/components': { View: 'view', Text: 'text', Button: 'button' },
    '../../components/ui': { Action: 'action', Field: 'field', Notice: 'notice', Sheet: 'sheet' },
    '../../services/api': { token: () => 'fixture', errorText: () => '失败' },
    '../../services/device-events': { deviceRevision: () => 0 },
    '../../platform/media': { mediaCapabilities: () => ({ platform: 'weapp', playback: true }), performMedia: async (intent: string) => { intents.push(intent); }, refreshAuthorizations: async () => ({}) },
  });
  return { tree: source.default(), navigation, intents, tabActions, cleanups, toasts };
}
test('设备入口顺序和更多菜单保留当前设备', () => {
  const { tree, navigation } = render('authorized', true, true);
  const links = nodes(tree).find(n => n.props?.className === 'device-feature-links')!;
  const buttons = nodes(links).filter(n => n.type === 'action');
  assert.equal(buttons.length, 3);
  for (const button of buttons) button.props.onClick();
  assert.deepEqual(navigation, ['roles', 'recognition', 'contacts'].map(page => '/pages/' + page + '/index?device_id=device%2Fa'));
  const room = nodes(tree).find(n => n.props?.title === '多人对讲')!;
  room.props.onClick();
  assert.equal(navigation.at(-1), '/pages/room/index?device_id=device%2Fa');
});
test('主按钮区分授权和呼叫；离线允许授权但不允许呼叫', async () => {
  for (const [state, online, label, disabled, intent] of [
    ['missing', false, '授权微信通话', false, 'authorize'],
    ['disabled', true, '开启通话提醒', false, 'authorize'],
    ['authorized', true, '微信通话', false, 'wechat-call'],

  ] as const) {
    const { tree, intents } = render(state, online);
    const group = nodes(tree).find(n => n.props?.className === 'device-actions device-primary-actions')!;
    const button = nodes(group).find(n => n.type === 'action')!;
    assert.ok(nodes(button).some(n => Array.isArray(n.props?.children) && n.props.children.includes(label)));
    assert.equal(button.props.disabled, disabled);
    if (!disabled) {
      button.props.onClick();
      await Promise.resolve();
      assert.deepEqual(intents, [intent]);
    }
  }
});

test('菜单打开隐藏导航，快速关闭后迟到的隐藏回调仍恢复导航', async () => {
  const closed = render('authorized', true, false);
  assert.deepEqual(closed.tabActions, []);
  const open = render('authorized', true, true);
  assert.deepEqual(open.tabActions, ['hide']);
  for (const cleanup of open.cleanups) cleanup();
  await Promise.resolve();
  assert.equal(open.tabActions.at(-1), 'show');
});

test('离线呼叫和播放显示轻提示但不启动媒体；授权和功能导航仍可用', async () => {
  const h = render('authorized', false);
  const group = nodes(h.tree).find(n => n.props?.className === 'device-actions device-primary-actions')!;
  const buttons = nodes(group).filter(n => n.type === 'action');
  for (const button of buttons) {
    assert.equal(button.props.disabled, false);
    assert.equal(button.props.muted, true);
    button.props.onClick();
    await Promise.resolve();
  }
  assert.deepEqual(h.intents, []);
  assert.deepEqual(h.toasts, ['设备已离线，请检查电源和网络', '设备已离线，请检查电源和网络']);
  const links = nodes(h.tree).find(n => n.props?.className === 'device-feature-links')!;
  for (const button of nodes(links).filter(n => n.type === 'action')) button.props.onClick();
  assert.equal(h.navigation.length, 3);
});
