import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

for (const [saved, suggested] of [[0, 60], [null, 0], [null, 60], [90, 60]] as const) {
  test('区分当前间隔与编辑建议值，当前间隔 ' + saved + '，建议间隔 ' + suggested, async () => {
    const state: unknown[] = [];
    let cursor = 0, onMount = () => {};
    const writes: {interval_seconds: number}[] = [];
    let rejectNext = false;
    const jsx = (type: unknown, props: any) => ({ type, props });
    const page = loadSource(fileURLToPath(new URL('../src/components/recognition-reminders.tsx', import.meta.url)), {
      'react/jsx-runtime': { jsx, jsxs: jsx },
      react: {
        useState(initial: unknown) {
          const i = cursor++;
          if (i >= state.length) state[i] = initial;
          return [state[i], (v: unknown) => { state[i] = v; }];
        },
        useEffect(fn: () => void) { onMount = fn; },
        useRef(current: unknown) { const i = cursor++; if (i >= state.length) state[i] = { current }; return state[i]; },
      },
      '@tarojs/components': { Button: 'preset', Input: 'input', Text: 'text', View: 'view' },
      './alarm-subscription': { DeviceAlarm: 'device-alarm' },
      './ui': { Action: 'button', Notice: 'notice' },
      '../services/api': {
        token: () => 'fixture', errorText: (e: Error) => e.message,
        request: async (_path: string, method?: string, data?: {interval_seconds: number}) => {
          if (method === 'PUT' && data) { writes.push(data); if (rejectNext) { rejectNext = false; throw Error('保存失败'); } return data; }
          return { interval_seconds: saved, suggested_seconds: suggested };
        },
      },
    });
    function nodes(value: any): any[] {
      if (!value || typeof value !== 'object') return [];
      if (Array.isArray(value)) return value.flatMap(nodes);
      return [value, ...nodes(value.props?.children)];
    }
    function render() { cursor = 0; return nodes(page.RecognitionReminders({ device: 'fixture', enabled: false })); }
    render(); onMount(); await new Promise(r => setImmediate(r));
    const input = () => render().find(n => n.type === 'input').props;
    assert.equal(input().value, String(saved ?? 0));
    const edit = (value: string) => input().onInput({detail: {value}});
    edit('1'); edit('12'); input().onBlur(); edit('120'); input().onConfirm();
    await new Promise(r => setImmediate(r));
    assert.deepEqual(writes.map(w => w.interval_seconds), [12, 120]);
    assert.equal(input().value, '120');
    edit(''); input().onBlur(); edit('86401'); input().onBlur();
    assert.equal(writes.length, 2);
    rejectNext = true;
    edit('0'); input().onBlur(); await new Promise(r => setImmediate(r));
    render().find(n => n.props.children === '重试保存间隔').props.onClick();
    await new Promise(r => setImmediate(r));
    assert.deepEqual(writes.map(w => w.interval_seconds), [12,120,0,0]);
    edit('45'); await new Promise(r => setTimeout(r, 750));
    assert.equal(writes.at(-1)?.interval_seconds, 45);
    const preset = () => render().find(n => n.type === 'preset' && n.props.children === '5 分钟');
    preset().props.onClick();
    assert.equal(writes.at(-1)?.interval_seconds, 300);
    assert.equal(input().value, '300');
    assert.match(preset().props.className, /active/);
    assert.ok(render().some(n => n.props.children === '保存中…'));
    await new Promise(r => setImmediate(r));
    assert.ok(render().some(n => n.props.children === '已保存'));
  });
}
