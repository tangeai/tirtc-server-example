import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

for (const [saved, suggested] of [[0, 60], [null, 0], [null, 60], [90, 60]] as const) {
  test('区分当前间隔与编辑建议值，当前间隔 ' + saved + '，建议间隔 ' + suggested, async () => {
    const state: unknown[] = [];
    let cursor = 0, onLoad = (_args: { device_id: string }) => {};
    const writes: unknown[] = [];
    const jsx = (type: unknown, props: any) => ({ type, props });
    const page = loadSource(fileURLToPath(new URL('../src/pages/notifications/index.tsx', import.meta.url)), {
      'react/jsx-runtime': { jsx, jsxs: jsx },
      react: {
        useState(initial: unknown) {
          const i = cursor++;
          if (i >= state.length) state[i] = initial;
          return [state[i], (v: unknown) => { state[i] = v; }];
        },
        useRef: (current: unknown) => ({ current }),
      },
      '@tarojs/taro': { useLoad: (cb: typeof onLoad) => { onLoad = cb; } },
      '@tarojs/components': { Input: 'input', Text: 'text', View: 'view' },
      '../../components/alarm-subscription': { DeviceAlarm: 'device-alarm' },
      '../../components/ui': { Action: 'button', Notice: 'notice' },
      '../../services/api': {
        token: () => 'fixture',
        request: async (_path: string, method?: string, data?: object) => {
          if (method === 'PUT') writes.push(data);
          return { interval_seconds: saved, suggested_seconds: suggested };
        },
      },
    });
    function nodes(value: any): any[] {
      if (!value || typeof value !== 'object') return [];
      if (Array.isArray(value)) return value.flatMap(nodes);
      return [value, ...nodes(value.props?.children)];
    }
    function render() { cursor = 0; return nodes(page.default()); }
    render(); onLoad({ device_id: 'fixture' }); await new Promise(r => setImmediate(r));
    assert.equal(render().find(n => n.props.className === 'reminder-interval-value').props.children, (saved ?? 0) + ' 秒');
    render().find(n => n.props.children === '设置提醒间隔').props.onClick();
    const editing = render();
    assert.equal(editing.find(n => n.type === 'input').props.value, String(saved ?? suggested));
    editing.find(n => n.props.children === '保存间隔').props.onClick();
    await new Promise(r => setImmediate(r));
    assert.equal(writes.length, 1);
    assert.equal((writes[0] as {interval_seconds: number}).interval_seconds, saved ?? suggested);
  });
}
