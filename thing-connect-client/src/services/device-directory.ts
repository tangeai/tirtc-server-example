import type { Device } from '../domain/devices';
import { ApiError, request, token } from './api';
import { deviceRevision } from './device-events';

// Account-local, memory-only snapshot. Explicit refresh and mutations invalidate
// in-flight reads as well as cached data; server endpoints still enforce ownership.
let session = '', revision = -1, generation = 0;
let snapshot: Device[] | undefined;
let pending: Promise<Device[]> | undefined;

export async function readDevices(refresh = false): Promise<Device[]> {
  const account = token(), changed = deviceRevision();
  if (session !== account || revision !== changed || refresh) {
    session = account; revision = changed; generation++;
    snapshot = undefined; pending = undefined;
  }
  if (!account) throw new ApiError(401, '请先登录');
  if (snapshot) return snapshot;
  if (pending) return pending;
  const current = generation;
  const operation = request<Device[]>('/v1/user/device/list').then(rows => {
    if (current !== generation || account !== token() || changed !== deviceRevision()) {
      throw new ApiError(-2, '设备信息已变化，请重新加载');
    }
    snapshot = rows.filter(row => row.status === 1);
    return snapshot;
  });
  pending = operation;
  try { return await operation; }
  finally { if (pending === operation) pending = undefined; }
}
