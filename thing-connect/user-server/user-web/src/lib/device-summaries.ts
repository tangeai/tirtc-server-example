import { ApiError, request } from './api';
import type { RoleBinding, RoomState, Summary } from './devices';

export async function readSummaries(devices: { device_id: string }[], current: () => boolean, publish: (id: string, summary: Summary) => void) {
  let cursor = 0;
  const roles = new Map<string, Promise<{ name: string }>>();
  async function worker() {
    while (cursor < devices.length && current()) {
      const device = devices[cursor++], id = encodeURIComponent(device.device_id);
      const summary: Summary = {};
      await Promise.allSettled([
        request<RoomState>('/v1/call/group/web/device/' + id).then(room => { summary.room = room; }),
        request<{ policy: { enabled: boolean }; schemes: unknown[] }>('/v1/recognition/devices/' + id + '/config').then(value => { summary.recognition = { enabled: value.policy.enabled, count: value.schemes.length }; }),
        (async () => {
          const binding = await request<RoleBinding>('/v1/ai/device/' + id + '/role');
          summary.role = { binding };
          if (!binding.role_id && !binding.default_role_id) return;
          const path = binding.role_id ? '/v1/ai/roles/' + encodeURIComponent(binding.role_id) : '/v1/ai/roles/default';
          if (!roles.has(path)) roles.set(path, request<{ name: string }>(path));
          try { summary.role.name = (await roles.get(path))?.name; }
          catch (e) { if (!(e instanceof ApiError) || ![403, 404, 40300, 40400].includes(e.code)) summary.role = undefined; }
        })(),
      ]);
      if (current()) publish(device.device_id, summary);
    }
  }
  await Promise.all(Array.from({ length: Math.min(4, devices.length) }, worker));
}
