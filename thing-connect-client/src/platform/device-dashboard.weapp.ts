import type { Device } from '../domain/devices';
export function useDeviceDashboard() {
  return { recover: async (): Promise<Device[] | undefined> => undefined, update(_rows: Device[]) {}, summary(_id: string) { return ''; }, restored: false };
}
