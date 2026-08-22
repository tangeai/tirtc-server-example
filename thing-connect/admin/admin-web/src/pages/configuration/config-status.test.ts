import assert from 'node:assert/strict';
import test from 'node:test';
import { commonServiceRows, configBadges } from './config-status.ts';

test('published MQTT shows usable state without blocking warnings', () => {
  const badges = configBadges(
    { required: true, blocking: true, test_kind: 'mqtt', reload: 'restart' },
    true,
  );

  assert.deepEqual(badges, [
    { color: 'success', label: '已测试可用' },
    { color: 'processing', label: '配置变更后需服务器重启' },
  ]);
});

test('unpublished blocking config explains the startup impact', () => {
  assert.deepEqual(
    configBadges({ required: true, blocking: true, test_kind: 'mqtt', reload: 'restart' }, false),
    [
      { color: 'gold', label: '必填' },
      { color: 'error', label: '未配置，将阻塞业务' },
      { color: 'processing', label: '配置后需服务器重启' },
    ],
  );
});

test('published config without an online probe only claims configured state', () => {
  assert.deepEqual(configBadges({ required: true, blocking: true, reload: 'restart' }, true), [
    { color: 'success', label: '已配置' },
    { color: 'processing', label: '配置变更后需服务器重启' },
  ]);
});

test('common config stays renderable when every service is offline', () => {
  const rows = commonServiceRows([
    {
      service: 'user-server',
      status: 'offline',
      instances: null,
    },
  ]);

  assert.deepEqual(rows, [
    {
      service: 'user-server',
      status: 'offline',
      instance_id: '—',
      node: '—',
    },
  ]);
});

test('common config keeps reported instances and accepts an empty list', () => {
  assert.deepEqual(
    commonServiceRows([
      { service: 'user-server', status: 'offline', instances: [] },
      {
        service: 'voip-server',
        status: 'healthy',
        instances: [{ instance_id: 'voip-1', node: 'node-1' }],
      },
    ]),
    [
      { service: 'user-server', status: 'offline', instance_id: '—', node: '—' },
      {
        service: 'voip-server',
        status: 'healthy',
        instance_id: 'voip-1',
        node: 'node-1',
      },
    ],
  );
});
