export interface MediaProfile {
  up_audio_mt?: string[] | string;
  down_audio_mt?: string[] | string;
  up_video_mt?: string[] | string;
  down_video_mt?: string[] | string;
  no_video?: boolean;
  has_camera?: boolean;
  has_screen?: boolean;
  camera_rotation?: number;
  down_video_rotation?: number;
  aspect_ratio?: number | string;
  object_fit?: string;
}
export interface Device {
  device_id: string;
  device_name: string;
  status: number;
  online: boolean;
  bind_time?: string;
  profiles?: { stream?: MediaProfile; voip?: MediaProfile; call?: MediaProfile };
}
export type AuthorizationState = 'unknown' | 'missing' | 'disabled' | 'authorized';
export type MediaIntent = 'playback' | 'authorize' | 'wechat-call';
export interface PlatformCapabilities { platform: 'weapp' | 'h5'; playback: boolean; authorization: boolean; wechatCall: boolean }
export type MediaDecision =
  | { action: 'blocked'; message: string }
  | { action: 'playback'; profile: MediaProfile }
  | { action: 'refresh-authorization' | 'authorize' | 'open-settings' }
  | { action: 'wechat-call'; roomType: 'voice' | 'video'; callerCamera: boolean; listenerCamera: boolean; profile: MediaProfile };

function video(value: string | string[] | undefined): boolean {
  return (Array.isArray(value) ? value : [value]).some(v => typeof v === 'string' && !!v.trim() && v.toLowerCase() !== 'none');
}

// An explicit user intent is never converted from playback into a phone call.
// Missing scene capabilities stay unknown; stream/call/voip must not borrow fields.
export function decideMedia(intent: MediaIntent, device: Device, authorization: AuthorizationState, platform: PlatformCapabilities): MediaDecision {
  const blocked = (message: string): MediaDecision => ({ action: 'blocked', message });
  if (intent === 'playback') {
    if (!device.online) return blocked('设备离线，请检查电源和网络');
    if (!platform.playback) return blocked('当前客户端暂不支持实时播放');
    if (!device.profiles?.stream) return blocked('设备尚未上报实时播放能力，请刷新后重试');
    return { action: 'playback', profile: device.profiles.stream };
  }
  if (platform.platform !== 'weapp' || !platform.authorization) return blocked('请在微信小程序中管理通话授权');
  if (intent === 'wechat-call') {
    if (!platform.wechatCall) return blocked('当前微信版本或客户端不支持设备通话');
    if (!device.online) return blocked('设备离线，请检查电源和网络');
  }
  if (authorization === 'unknown') return { action: 'refresh-authorization' };
  if (authorization === 'disabled' || (intent === 'authorize' && authorization === 'authorized')) return { action: 'open-settings' };
  if (authorization === 'missing') return { action: 'authorize' };
  const profile = device.profiles?.voip;
  if (!profile) return blocked('设备尚未上报微信通话能力，请刷新后重试');
  const listenerCamera = profile.no_video !== true && (profile.has_camera ?? video(profile.up_video_mt));
  const callerCamera = profile.no_video !== true && (profile.has_screen ?? video(profile.down_video_mt));
  return { action: 'wechat-call', roomType: callerCamera || listenerCamera ? 'video' : 'voice', callerCamera, listenerCamera, profile };
}
