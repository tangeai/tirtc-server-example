import type { Device } from '../domain/devices';
import { request } from './api';

export interface CardState {
  roleName?: string;
  hasRole?: boolean;
  recognition?: boolean;
}

export async function readCardStates(devices: Device[], current: () => boolean, publish: (id: string, state: CardState) => void) {
  let next = 0;
  const names = new Map<string, Promise<{ name: string }>>();
  async function worker() {
    while (next < devices.length && current()) {
      const device = devices[next++], id = encodeURIComponent(device.device_id), state: CardState = {};
      await Promise.allSettled([
        (async () => {
          const binding = await request<{ role_id?: string; default_role_id?: string }>('/v1/ai/device/' + id + '/role');
          if (!binding.role_id && !binding.default_role_id) { state.hasRole = false; return; }
          const path = binding.role_id ? '/v1/ai/roles/' + encodeURIComponent(binding.role_id) : '/v1/ai/roles/default';
          if (!names.has(path)) names.set(path, request<{ name: string }>(path));
          const role = await names.get(path);
          if (role?.name) { state.roleName = role.name; state.hasRole = true; }
        })(),
        request<{ policy: { enabled: boolean } }>('/v1/recognition/devices/' + id + '/config')
          .then(value => { state.recognition = value.policy.enabled; }),
      ]);
      if (current()) publish(device.device_id, state);
    }
  }
  await Promise.all(Array.from({ length: Math.min(3, devices.length) }, worker));
}
