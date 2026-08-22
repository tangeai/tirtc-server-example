import assert from 'node:assert/strict';
import test from 'node:test';
import { commonServiceRows } from './config-status.ts';

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
