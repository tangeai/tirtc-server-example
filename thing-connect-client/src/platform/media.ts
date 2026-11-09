import type { AuthorizationState, Device, MediaIntent, PlatformCapabilities } from '../domain/devices';
import { ApiError } from '../domain/http';
export interface DeviceAuthorization { state: AuthorizationState; serverSynced: boolean; authorizedName?: string }
export function initializeMedia() {}
export function refreshIncomingMedia() {}
export function cacheDeviceMedia(_devices: Device[]) {}
export function mediaCapabilities(): PlatformCapabilities { return { platform: 'h5', playback: false, authorization: false, wechatCall: false }; }
export async function refreshAuthorizations(_devices: Device[]): Promise<Record<string, DeviceAuthorization>> { return {}; }
export async function performMedia(_intent: MediaIntent, _device: Device, _authorization: DeviceAuthorization): Promise<void> {
  throw new ApiError(-1, '此功能请在微信小程序中使用');
}
