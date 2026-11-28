import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';

type Options = {
  choice?: string; failure?: string; errCode?: number; code?: number; replace?: boolean; approve?: boolean;
  granted?: boolean; mismatch?: boolean; legacy?: boolean; prepareFailure?: boolean; holdPrompt?: boolean;
  loseAcknowledgement?: boolean; refreshFailure?: boolean;
};
function harness(o: Options = {}) {
  let cursor = 0, mounted = false, sessions = 0, prompts = 0, confirms = 0, modal = 0, effects: (() => void)[] = [];
  const cells: any[] = [], decisions: string[] = [];
  const acceptedReports = new Map<string, string>();
  let granted = !!o.granted, sequence = 0, currentDecision = 0, finishPrompt = () => {};
  let now = Date.now(), ticketCount = 0;
  class ApiError extends Error { code: number; constructor(code: number, msg: string) { super(msg); this.code = code; } }
  const jsx = (type: any, props: any) => ({ type, props });
  const source = loadSource(fileURLToPath(new URL('../src/components/alarm-subscription.tsx', import.meta.url)), {
    '@tarojs/components': { Text: 'text', View: 'view' }, 'react/jsx-runtime': { jsx, jsxs: jsx }, './ui': { Notice: 'notice', Action: 'button' },
    react: {
      useState(initial: unknown) { const i = cursor++; if (!mounted) cells[i] = initial; return [cells[i], (v: unknown) => { cells[i] = v; }]; },
      useRef(initial: unknown) { const i = cursor++; if (!mounted) cells[i] = { current: initial }; return cells[i]; },
      useEffect(fn: () => void) { if (!mounted) effects.push(fn); },
    },
    '@tarojs/taro': { __esModule: true, default: { login: async () => ({ code: 'fixture' }), getAccountInfoSync: () => ({ miniProgram: { appId: 'app' } }), showModal: async (dialog: { content: string }) => {
        assert.match(dialog.content, /其他账号/); assert.match(dialog.content, /本账号原有/); assert.match(dialog.content, /其他已授权设备需要重新授权/); assert.match(dialog.content, /等待发送/);
        modal++; return { confirm: o.approve };
      } } },
    '../services/api': { token: () => 'account', ApiError, errorText: (e: Error) => e.message, request: async (path: string, method: string, body?: {decision: string; device_id?: string; model_id?: string}) => {
      if (path.endsWith('/sn-ticket')) return { sn_ticket: 'ticket-' + ++ticketCount, model_id: 'model' };
      if (path.endsWith('/capabilities?device_id=device-A') && o.refreshFailure && decisions.length) throw new ApiError(50300, '读取状态失败');
      if (path.endsWith('/capabilities?device_id=device-A')) return o.legacy ? { alarm: true } : { device_id: 'device-A', alarm: true, alarm_template_id: 'template', alarm_subscribed: granted };
      if (path.endsWith('/device-subscriptions')) {
        assert.equal(body?.device_id, 'device-A'); assert.equal(body?.model_id, 'model');
        sessions++;
        if (o.prepareFailure && sessions === 1) throw new ApiError(50300, '创建会话失败');
        return { session_id: String(++sequence), replace: o.replace, template_id: o.mismatch ? 'new' : 'template' };
      }
      if (path.endsWith('/confirm')) { confirms++; return; }
      assert.ok(path.endsWith('/result')); assert.equal(method, 'POST'); decisions.push(body!.decision);
      if (o.code && decisions.length === 1) throw new ApiError(o.code, '保存失败');
      if (!acceptedReports.has(path)) {
        const session = Number(path.split('/').at(-2));
        if (session < currentDecision) throw new ApiError(40901, '已有较新的决定');
        acceptedReports.set(path, body!.decision);
        if (body!.decision !== 'cancel') { granted = body!.decision === 'accept'; currentDecision = session; }
      }
      if (o.loseAcknowledgement && decisions.length === 1) throw new ApiError(-1, '回执丢失');
    } },
  }, { Date: { now: () => now }, wx: { requestSubscribeDeviceMessage(a: any) {
    prompts++; assert.deepEqual(Array.from(a.tmplIds), ['template']); assert.equal(a.sn, 'device-A'); assert.equal(a.snTicket, 'ticket-' + ticketCount); assert.equal(a.modelId, 'model');
    finishPrompt = () => {
      if (o.failure) a.fail({ errMsg: o.failure, errCode: o.errCode }); else a.success({ template: o.choice || 'accept' });
    };
    if (!o.holdPrompt) finishPrompt();
  } } });
  function render() {
    cursor = 0; const alarm = source.useAlarmSubscription('device-A'); mounted = true;
    for (const effect of effects) effect(); effects = [];
    return alarm;
  }
  render();
  return { render, decisions, acceptedReports, counts: () => ({ sessions, prompts, confirms, modal }),
    advanceTime: (ms: number) => { now += ms; },
    finishPrompt: () => finishPrompt(), changeRemoteGrant: (value: boolean) => { granted = value; currentDecision = ++sequence; } };
}
const flush = () => new Promise<void>(resolve => setImmediate(resolve));

for (const [value, decision] of [['acceptWithAudio','accept'], ['accept','accept'], ['reject','reject'], ['ban','reject'], ['cancel','cancel']]) {
  test('点击一次按微信结果保存 ' + value, async () => {
    const h = harness({ choice: value }); await flush();
    const done = h.render().activate(); assert.equal(h.counts().prompts, 1); await done;
    assert.deepEqual(h.decisions, [decision]);
    assert.equal(h.render().channel.alarm_subscribed, decision === 'accept');
  });
}
for (const code of [50300, 40901, 41000]) {
  test('保存失败后重试 ' + code, async () => {
    const h = harness({ code }); await flush(); await h.render().activate();
    assert.equal(h.render().pending, code === 50300);
    await h.render().activate();
    assert.deepEqual(h.counts(), { sessions: code === 50300 ? 1 : 2, prompts: code === 50300 ? 1 : 2, confirms: code === 50300 ? 1 : 2, modal: 0 });
    assert.equal(h.render().channel.alarm_subscribed, true);
  });
}
for (const approve of [true,false]) {
  test('确认替换接收微信 ' + approve, async () => {
    const h = harness({ replace: true, approve }); await flush(); await h.render().activate();
    assert.equal(h.counts().modal, 1); assert.equal(h.counts().confirms, approve ? 1 : 0);
  });
}
test('原生取消保留原授权，手机失败显示错误码', async () => {
  const c = harness({ granted: true, failure: 'requestSubscribeMessage:fail cancel' }); await flush();
  await c.render().refresh(true); await c.render().activate(); assert.deepEqual(c.decisions, ['cancel']); assert.equal(c.render().channel.alarm_subscribed, true);
  const h = harness({ failure: 'requestSubscribeMessage:fail', errCode: 20004 }); await flush();
  await h.render().activate(); assert.match(h.render().notice, /20004/); assert.equal(h.counts().confirms, 0); assert.deepEqual(h.decisions, []);
});
test('模板变更时旧授权不能写入新模板', async () => {
  const h = harness({ mismatch: true }); await flush(); await h.render().activate();
  assert.equal(h.counts().confirms, 0); assert.equal(h.render().pending, false);
});
test('准备失败后只补建会话，等待用户再次点击才打开微信', async () => {
  const h = harness({ prepareFailure: true }); await flush();
  assert.equal(h.render().unavailable, true);
  await h.render().activate();
  assert.equal(h.counts().prompts, 0); assert.deepEqual(h.decisions, []);
  await h.render().activate();
  assert.equal(h.counts().prompts, 1); assert.deepEqual(h.decisions, ['accept']);
});
test('用户停留在授权窗期间，旧同意无法覆盖另一客户端较新的拒绝', async () => {
  const h = harness({ holdPrompt: true }); await flush();
  const first = h.render().activate();
  assert.deepEqual(h.counts(), { prompts: 1, sessions: 1, confirms: 0, modal: 0 });
  h.changeRemoteGrant(false); h.finishPrompt(); await first;
  assert.equal(h.acceptedReports.size, 0);
  assert.equal(h.render().channel.alarm_subscribed, false);
  assert.equal(h.render().pending, false);
});
test('设备列表久置后重新准备会话，过期会话不会接收新的决定', async () => {
  const h = harness(); await flush(); h.advanceTime(5 * 60 * 1000);
  await h.render().activate(); assert.equal(h.counts().prompts, 0);
  assert.equal(h.render().pending, false); assert.match(h.render().notice, /点击/);
  await h.render().activate(); assert.equal(h.counts().prompts, 1);
  assert.deepEqual([...h.acceptedReports.keys()], ['/v1/push/user/subscriptions/2/result']);
});
test('旧会话回执丢失后重试成功，仍显示服务端当前的较新授权状态', async () => {
  const h = harness({ loseAcknowledgement: true }); await flush();
  await h.render().activate(); assert.equal(h.render().pending, true);
  h.changeRemoteGrant(false);
  await h.render().activate();
  assert.deepEqual(h.decisions, ['accept', 'accept']); assert.equal(h.acceptedReports.size, 1);
  assert.equal(h.counts().prompts, 1); assert.equal(h.render().channel.alarm_subscribed, false);
});
test('旧推送服务未返回设备范围时拒绝发起授权', async () => {
  const h = harness({ legacy: true }); await flush();
  await h.render().activate();
  assert.equal(h.counts().prompts, 0);
  assert.equal(h.counts().sessions, 0);
});

for (const errCode of [19720726, 19720727]) {
  test('微信拒绝设备票据后下次点击使用新会话和新票据 ' + errCode, async () => {
    const options = { failure: 'requestSubscribeDeviceMessage:fail', errCode };
    const h = harness(options); await flush(); await h.render().activate();
    assert.equal(h.counts().sessions, 2); assert.equal(h.counts().prompts, 1);
    assert.equal(h.counts().confirms, 0); assert.deepEqual(h.decisions, []);
    options.failure = '';
    await h.render().activate();
    assert.deepEqual([...h.acceptedReports.keys()], ['/v1/push/user/subscriptions/2/result']);
  });
}
test('新票据仍未通过校验时显示配置排查提示', async () => {
  const h = harness({ failure: 'requestSubscribeDeviceMessage:fail', errCode: 19720726 });
  await flush(); await h.render().activate(); await h.render().activate();
  assert.equal(h.counts().sessions, 2); assert.equal(h.counts().confirms, 0);
  assert.equal(h.render().unavailable, true); assert.match(h.render().notice, /AppID/);
});

test('取消后回读失败显示真正的错误', async () => {
  const h = harness({ choice: 'cancel', refreshFailure: true }); await flush();
  await h.render().activate();
  assert.equal(h.render().failed, true);
  assert.equal(h.render().notice, '读取状态失败');
});
