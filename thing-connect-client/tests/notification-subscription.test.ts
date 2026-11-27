import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

const cancelled = '已取消本次订阅操作，原有提醒状态不变';
const cases = [
  { value: 'accept', decision: 'accept', notice: '设备提醒已订阅' },
  { value: 'reject', decision: 'reject', notice: '已拒绝设备提醒' },
  { value: 'ban', decision: 'reject', notice: '已拒绝设备提醒' },
  { value: 'cancel', decision: 'cancel', notice: cancelled },
  { failure: 'requestSubscribeMessage:fail cancel', decision: 'cancel', notice: cancelled },
  { failure: 'requestSubscribeMessage:fail network', notice: '微信订阅未完成，请重试' },
  { value: 'cancel', decision: 'cancel', code: 40901 },
  ...[40901, 41000, 50300].map(code => ({ value: 'accept', decision: 'accept', code })),
];
type Scenario = { value?: string; failure?: string; decision?: string; code?: number; notice?: string };
for (const {value, failure, decision, code, notice} of cases as Scenario[]) {
  test(`微信 ${failure ?? value}，回报 ${code ?? '正常'}`, async () => {
    class ApiError extends Error {
      code: number;
      constructor(code: number, msg: string) { super(msg); this.code = code; }
    }
    const states: unknown[] = [];
    let cursor = 0, load = (_options: { device_id: string }) => {}, sessions = 0, reports = 0;
    const jsx = (type: unknown, props: unknown) => ({ type, props });
    const source = loadSource(fileURLToPath(new URL('../src/pages/notifications/index.tsx', import.meta.url)), {
      react: { useRef: () => ({ current: false }), useState: (initial: unknown) => {
        const i = cursor++;
        if (i >= states.length) states.push(initial);
        return [states[i], (value: unknown) => { states[i] = value; }];
      } },
      'react/jsx-runtime': { jsx, jsxs: jsx },
      '@tarojs/taro': { __esModule: true, default: { login: async () => ({ code: 'login' }), getAccountInfoSync: () => ({ miniProgram: { appId: 'app' } }) }, useLoad: (hook: typeof load) => { load = hook; } },
      '@tarojs/components': { Input: 'input', Text: 'text', View: 'view' },
      '../../components/ui': { Action: 'action', Notice: 'notice' },
      '../../services/api': { ApiError, token: () => 'token', errorText: (e: Error) => e.message, request: async (path: string, _method: string, body?: {decision: string}) => {
        if (path.endsWith('/subscriptions')) return { session_id: String(++sessions), template_id: 'template' };
        if (path.endsWith('/result')) {
          assert.equal(body?.decision, decision);
          reports++;
          if (code && reports === 1) throw new ApiError(code, '保存失败');
        }
        return { enabled: true, interval_seconds: null };
      } },
    }, { wx: { requestSubscribeMessage: (args: {success: (v: object) => void; fail: (e: object) => void}) => failure ? args.fail({errMsg: failure}) : args.success({template: value}) } });
    const render = () => { cursor = 0; return source.default(); };
    const action = () => render().props.children.at(-1);
    const flush = () => new Promise<void>(resolve => setImmediate(resolve));
    render(); load({ device_id: 'dev' }); await flush();
    action().props.onClick(); await flush();
    action().props.onClick(); await flush();
    if (!code) {
      const notices = render().props.children.filter((child: {type?: string}) => child?.type === 'notice');
      assert.equal(notices.find((child: {props: {error?: boolean}}) => !!child.props.error === !decision).props.children, notice);
      assert.equal(reports, decision ? 1 : 0);
      assert.equal(action().props.children.trim(), decision ? '订阅 AI 异常提醒' : '确认订阅微信提醒');
      return;
    }
    assert.equal(action().props.children.trim(), code === 50300 ? '重试保存订阅结果' : '订阅 AI 异常提醒');
    action().props.onClick(); await flush();
    assert.deepEqual([sessions, reports], code === 50300 ? [1, 2] : [2, 1]);
  });
}
