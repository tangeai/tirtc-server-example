import assert from 'node:assert/strict';
import { test } from 'node:test';
import { decideMedia, type Device, type PlatformCapabilities } from '../src/domain/devices.ts';
const weapp: PlatformCapabilities = { platform: 'weapp', playback: true, authorization: true, wechatCall: true };
const device: Device = { device_id: 'fixture-device', device_name: '测试设备', status: 1, online: true, profiles: { stream: { up_video_mt: ['h264'] }, voip: { up_video_mt: 'none', down_video_mt: 'none' } } };
test('播放不依赖微信通话授权，不借用 voip profile', () => {
  for (const state of ['unknown', 'missing', 'disabled', 'authorized'] as const) {
    assert.deepEqual(decideMedia('playback', device, state, weapp), { action: 'playback', profile: device.profiles!.stream });
  }
  assert.equal(decideMedia('playback', { ...device, profiles: { voip: { up_video_mt: 'h264' } } }, 'authorized', weapp).action, 'blocked');
});
test('实时流有视频不代表微信通话有视频', () => {
  const result = decideMedia('wechat-call', device, 'authorized', weapp);
  assert.equal(result.action, 'wechat-call');
  if (result.action === 'wechat-call') { assert.equal(result.roomType, 'voice'); assert.equal(result.callerCamera, false); assert.equal(result.listenerCamera, false); }
});
test('设备摄像头对应被叫画面，屏幕对应手机主叫摄像头', () => {
  for (const [profile, callerCamera, listenerCamera] of [
    [{ up_video_mt: 'h264' }, false, true], [{ down_video_mt: 'mjpeg' }, true, false],
    [{ up_video_mt: 'h264', down_video_mt: 'mjpeg' }, true, true],
    [{ up_video_mt: 'h264', has_camera: false }, false, false],
  ] as const) {
    const result = decideMedia('wechat-call', { ...device, profiles: { voip: profile } }, 'authorized', weapp);
    assert.equal(result.action, 'wechat-call');
    if (result.action === 'wechat-call') { assert.equal(result.callerCamera, callerCamera); assert.equal(result.listenerCamera, listenerCamera); }
  }
});
test('未授权、授权关闭、读取失败不能混为一谈', () => {
  assert.equal(decideMedia('wechat-call', device, 'unknown', weapp).action, 'refresh-authorization');
  assert.equal(decideMedia('wechat-call', device, 'missing', weapp).action, 'authorize');
  assert.equal(decideMedia('wechat-call', device, 'disabled', weapp).action, 'open-settings');
  assert.equal(decideMedia('authorize', { ...device, online: false }, 'missing', weapp).action, 'authorize');
  assert.equal(decideMedia('wechat-call', { ...device, online: false }, 'authorized', weapp).action, 'blocked');
});
test('浏览器不调用微信授权或通话，能力缺失不静默降级为另一种业务', () => {
  const web: PlatformCapabilities = { platform: 'h5', playback: true, authorization: false, wechatCall: false };
  assert.equal(decideMedia('wechat-call', device, 'authorized', web).action, 'blocked');
  assert.equal(decideMedia('authorize', device, 'missing', web).action, 'blocked');
  assert.equal(decideMedia('playback', device, 'missing', web).action, 'playback');
  assert.equal(decideMedia('playback', device, 'authorized', { ...weapp, playback: false }).action, 'blocked');
});
