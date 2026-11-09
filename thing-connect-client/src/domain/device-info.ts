import type { MediaProfile } from './devices.ts';

export interface CapabilityRow { label: string; value: string }
function media(value: string | string[] | undefined): boolean | undefined {
  if (value === undefined) return undefined;
  return (Array.isArray(value) ? value : [value]).some(item => !!item.trim() && item.trim().toLowerCase() !== 'none');
}
const status = (value: boolean | undefined) => value === undefined ? '未上报' : value ? '支持' : '不支持';
export function capabilityRows(profile?: MediaProfile): CapabilityRow[] {
  if (!profile) return [];
  const camera = profile.no_video === true ? false : profile.has_camera ?? media(profile.up_video_mt);
  const screen = profile.no_video === true ? false : profile.has_screen ?? media(profile.down_video_mt);
  return [
    { label: '设备发送声音', value: status(media(profile.up_audio_mt)) },
    { label: '设备接收声音', value: status(media(profile.down_audio_mt)) },
    { label: '设备发送画面', value: status(camera) },
    { label: '设备接收画面', value: status(screen) },
  ];
}
