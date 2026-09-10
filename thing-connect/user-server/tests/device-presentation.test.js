const test = require('node:test');
const assert = require('node:assert/strict');
const view = require('../static/device-presentation.js');

test('missing scene stays unreported and never borrows VoIP values', () => {
  const profiles = {voip: {down_audio_mt: 'opus,amr', audio_rate: 16000}};
  assert.equal(view.mediaRows(profiles, 'stream'), null);
  assert.equal(view.mediaRows(profiles, 'call'), null);
  const rows = Object.fromEntries(view.mediaRows(profiles, 'voip'));
  assert.equal(rows['上行音频'], '未上报');
  assert.equal(rows['下行音频（首选顺序）'], 'Opus / AMR');
});

test('explicit zero, false, empty video and missing data remain distinct', () => {
  const rows = Object.fromEntries(view.mediaRows({voip: {camera_rotation: 0, hor_mirror: false, down_video_mt: '', audio_channels: 1}}, 'voip'));
  assert.equal(rows['摄像头旋转'], '0°（顺时针）');
  assert.equal(rows['水平镜像'], '否');
  assert.equal(rows['垂直镜像'], '未上报');
  assert.equal(rows['下行视频（首选顺序）'], '无');
  assert.equal(rows['上行视频'], '未上报');
});

test('VoIP downlink video rotation uses product-facing labels and defaults to zero', () => {
  for (const [value, expected] of [[undefined, '默认'], [0, '默认'], [1, '正向画面'], [2, '保留旋转画面']]) {
    const profile = {camera_rotation: 0};
    if (value !== undefined) profile.down_video_rotation = value;
    const rows = Object.fromEntries(view.mediaRows({voip: profile}, 'voip'));
    assert.equal(rows['下行视频方向'], expected);
  }
  const streamRows = Object.fromEntries(view.mediaRows({stream: {camera_rotation: 0}}, 'stream'));
  assert.equal(streamRows['下行视频方向'], undefined);
});

test('room summary preserves leading zeros and reports deferred work', () => {
  const room = {desired_state:'joined', room_code:'001234', online_count:3, state:'joined'};
  assert.equal(view.roomSummary(room), '001234 · 3 人在线');
  assert.equal(view.roomSummary({...room, state:'waiting_device'}), '001234');
  assert.match(view.roomSummary({...room, state:'suspended'}), /空闲后加入/);
  assert.equal(view.roomSummary({desired_state:'left'}), '当前未加入房间');
});

test('role summary differentiates unassigned, deleted and renamed roles', () => {
  assert.equal(view.roleSummary({role_id:''}), '未配置默认角色');
  assert.equal(view.roleSummary({role_id:'r1'}, null), '角色不可用，请重新选择');
  assert.equal(view.roleSummary({role_id:'r1'}, {name:'改名后的角色'}), '当前：改名后的角色');
});


test('unassigned devices display the default role without disguising missing roles', () => {
  const binding = {role_id: '', default_role_id: 'default-1'};
  assert.equal(view.roleSummary(binding, {name: '默认助手'}), '默认助手（默认）');
  assert.equal(view.roleSummary(binding, null), '默认角色不可用');
  assert.equal(view.roleSummary({...binding, role_id: 'custom-1'}, {name: '我的助手'}), '当前：我的助手');
});
