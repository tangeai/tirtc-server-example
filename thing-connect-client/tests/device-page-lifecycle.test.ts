import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

test('设备页失败后返回不自动重试；手动刷新和绑定事件可更新，重复刷新不重入', async () => {
  let show = () => {}, revision = 0, requests = 0, fail = true;
  let resolve: (value: unknown) => void = () => {};
  const jsx = (type: unknown, props: any) => ({ type, props });
  const source = loadSource(fileURLToPath(new URL('../src/pages/devices/index.tsx', import.meta.url)), {
    'react/jsx-runtime': { jsx, jsxs: jsx },
    react: { useState: (value: unknown) => [value, () => {}], useRef: (value: unknown) => ({ current: value }), useEffect: (fn: () => void) => fn() },
    '@tarojs/taro': { __esModule: true, default: {}, useDidShow: (fn: typeof show) => { show = fn; } },
    '@tarojs/components': { View: 'view', Text: 'text' },
    '../../components/ui': { Action: 'action', Field: 'field', Notice: 'notice', Sheet: 'sheet' },
    '../../services/api': { token: () => 'fixture', errorText: () => '网络失败', request: () => {
      requests++; return fail ? Promise.reject(new Error('offline')) : new Promise(r => { resolve = r; });
    } },
    '../../services/device-events': { deviceRevision: () => revision },
    '../../platform/media': { mediaCapabilities: () => ({ platform: 'weapp' }), cacheDeviceMedia() {}, refreshAuthorizations: async () => ({}) },
  });
  const tree = source.default();
  function find(node: any): any {
    if (!node || typeof node !== 'object') return;
    if (Array.isArray(node)) return node.map(find).find(Boolean);
    return node.props?.children === '刷新' ? node : find(node.props?.children);
  }
  const flush = async () => { for (let i = 0; i < 6; i++) await Promise.resolve(); };
  show(); await flush(); assert.equal(requests, 1);
  show(); await flush(); assert.equal(requests, 1, '返回失败页面不得偷偷发请求');
  fail = false;
  find(tree).props.onClick(); find(tree).props.onClick();
  assert.equal(requests, 2, '连续点击只启动一次请求');
  resolve([]); await flush();
  show(); assert.equal(requests, 2);
  revision++; show(); assert.equal(requests, 3, '绑定等显式修改允许更新列表');
  resolve([]); await flush();
});
