import assert from 'node:assert/strict';
import test from 'node:test';
import { capabilityRows } from '../src/domain/device-info.ts';
test('未上报不能显示为不支持；明确空列表和 none 才表示不支持', () => {
  assert.deepEqual(capabilityRows(), []);
  assert.deepEqual(capabilityRows({}).map(row => row.value), ['未上报', '未上报', '未上报', '未上报']);
  assert.deepEqual(capabilityRows({ up_audio_mt: ['alaw'], down_audio_mt: 'none', up_video_mt: [], down_video_mt: ' H264 ' }).map(row => row.value), ['支持', '不支持', '不支持', '支持']);
});
test('禁用视频优先于视频编码，明确派生能力优先于编码推断', () => {
  assert.deepEqual(capabilityRows({ no_video: true, has_camera: true, up_video_mt: 'h264', down_video_mt: 'mjpeg' }).slice(2).map(row => row.value), ['不支持', '不支持']);
  assert.equal(capabilityRows({ has_camera: false, up_video_mt: 'h264' })[2].value, '不支持');
});
