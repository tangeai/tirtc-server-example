import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';
function harness(platform = 'ios') {
  let event: (value: object) => void = () => {}, count = 0;
  const requests: { path: string; body: any }[] = [], navigations: string[] = [], options: any[] = [];
  let failNavigation = false, failHangup = false, failCancel = false;
  const hangups: string[] = [], toasts: string[] = [];
  class ApiError extends Error { code: number; constructor(code: number, message: string) { super(message); this.code = code; } }
  const plugin = { CALL_PAGE_PATH: 'plugin://voip/call', callDevice: async (value: unknown) => { options.push(value); return { roomId: 'room-' + ++count }; }, forceHangUpVoip(room: string) { hangups.push(room); if (failHangup) throw new Error('hangup failed'); event({ eventName: 'cancelVoip', roomId: room }); }, setUIConfig() {}, onVoipEvent(fn: typeof event) { event = fn; } };
  const api = loadSource(fileURLToPath(new URL('../src/platform/media.weapp.ts', import.meta.url)), {
    '@tarojs/taro': { __esModule: true, default: {
      getDeviceInfo: () => ({ platform }), getAccountInfoSync: () => ({ miniProgram: { appId: 'fixture-app' } }), login: async () => ({ code: 'fixture-code' }),
      navigateTo: async ({ url }: { url: string }) => { if (failNavigation) throw new Error('navigation failed'); navigations.push(url); }, showToast({ title }: { title: string }) { toasts.push(title); }, openSetting: async () => {},
    } },
    '../services/api': { ApiError, token: () => 'fixture-session', request: async (path: string, _method: string, body: unknown) => { requests.push({ path, body }); if (failCancel && path.endsWith('/cancel')) throw new Error('cancel failed'); return /wechat-mini-login/.test(path) ? { wx_user_openid: 'fixture-openid' } : {}; } },
    '../generated/voip-video-profile': { buildVideoUIConfig: () => ({}), cachedProfile() {}, incomingDeviceID() {}, updateDeviceVideoProfileCache() {}, downVideoRotationOption: () => ({}) },
    '../generated/voip-ui-config': { buildVoipUIConfig: () => ({}), CALL_DIRECTION: {}, phoneDisplaySnapshot: () => ({}) },
    '../generated/voip-incoming-query': { parseIncomingQuery: () => ({}) },
  }, { requirePlugin: () => ({ default: plugin }), wx: { getDeviceVoIPList() {}, requestDeviceVoIP() {} }, WX_MODEL_ID: 'fixture-model' });
  api.initializeMedia();
  return { api, requests, navigations, options, hangups, toasts, failHangup: () => { failHangup = true; }, failCancel: () => { failCancel = true; }, emit: (value: object) => event(value), failNavigation: () => { failNavigation = true; } };
}
const device = { device_id: 'fixture-device', device_name: '测试设备', online: true, status: 1, profiles: { stream: { up_video_mt: ['h264'] }, voip: { no_video: true } } };
const auth = { state: 'authorized', serverSynced: true };
test('模拟器通话明确拦截且不创建呼叫；实时播放仍能打开', async () => {
  const h = harness('devtools');
  await assert.rejects(h.api.performMedia('wechat-call', device, auth), /手机微信/);
  assert.equal(h.requests.length, 0); assert.equal(h.options.length, 0);
  await h.api.performMedia('playback', device, auth);
  assert.match(h.navigations[0], /pages\/playback/);
});
test('旧房间回调不结束新会话，正确挂断释放后可以再呼叫', async () => {
  const h = harness(); await h.api.performMedia('wechat-call', device, auth);
  assert.equal(h.options[0].roomType, 'voice');
  h.emit({ eventName: 'hangUpVoip', roomId: 'old-room' });
  await assert.rejects(h.api.performMedia('wechat-call', device, auth), /已有通话/);
  h.emit({ eventName: 'hangUpVoip', roomId: 'room-1' });
  await h.api.performMedia('wechat-call', device, auth);
  const cancel = h.requests.filter(r => r.path.endsWith('/cancel'));
  assert.equal(cancel.length, 1); assert.equal(cancel[0].body.wx_room_id, 'room-1'); assert.equal(h.options.length, 2);
  h.emit({ eventName: 'finishVoip', roomId: 'room-2' });
});
test('通话页打开失败发送取消，不遗留本地通话锁', async () => {
  const h = harness(); h.failNavigation();
  await assert.rejects(h.api.performMedia('wechat-call', device, auth), /通话页面打开失败/);
  await assert.rejects(h.api.performMedia('wechat-call', device, auth), /通话页面打开失败/);
  assert.equal(h.options.length, 2); assert.equal(h.requests.filter(r => r.path.endsWith('/cancel')).length, 2);
});
test('连续一百轮呼叫和结束无锁残留，同一时刻重复点击只创建一间房', async () => {
  const h = harness();
  for (let i = 1; i <= 100; i++) {
    const results = await Promise.allSettled([
      h.api.performMedia('wechat-call', device, auth),
      h.api.performMedia('wechat-call', device, auth),
    ]);
    assert.equal(results.filter(r => r.status === 'fulfilled').length, 1);
    assert.equal(h.options.length, i);
    h.emit({ eventName: 'hangUpVoip', roomId: 'room-' + i });
  }
  assert.equal(h.requests.filter(r => r.path.endsWith('/cancel')).length, 100);
});

for (const eventName of ['busy', 'timeout']) {
  test(eventName + ' 释放当前房间，重复和旧事件不影响下一通', async () => {
    const h = harness(); await h.api.performMedia('wechat-call', device, auth);
    h.emit({ eventName, roomId: 'room-1' });
    assert.equal(h.toasts.length, 1);
    h.emit({ eventName, roomId: 'room-1' });
    h.emit({ eventName: 'endVoip', roomId: 'room-1' });
    assert.equal(h.toasts.length, 1);
    await h.api.performMedia('wechat-call', device, auth);
    h.emit({ eventName, roomId: 'room-1' });
    await assert.rejects(h.api.performMedia('wechat-call', device, auth), /已有通话/);
    h.emit({ eventName: 'endVoip', roomId: 'room-2' });
  });
}
for (const failure of ['none', 'plugin', 'server']) {
  test('跳转失败独立清理插件和服务端：' + failure, async () => {
    const h = harness(); h.failNavigation();
    if (failure === 'plugin') h.failHangup();
    if (failure === 'server') h.failCancel();
    await assert.rejects(h.api.performMedia('wechat-call', device, auth), /通话页面打开失败/);
    assert.deepEqual(h.hangups, ['room-1']);
    assert.equal(h.requests.filter(r => r.path.endsWith('/cancel')).length, 1);
    await assert.rejects(h.api.performMedia('wechat-call', device, auth), /通话页面打开失败/);
    assert.equal(h.options.length, 2);
  });
}
