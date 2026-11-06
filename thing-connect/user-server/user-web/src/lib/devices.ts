import { t } from './i18n';
export interface Device {
  device_id: string; device_name?: string; online?: boolean; status?: number;
  mac?: string; hardware?: { chip_model?: string; board_model?: string }; firmware_version?: string; bind_time?: string;
  profiles?: Record<string, Record<string, unknown>>;
}
export interface RoomState { desired_state: string; state: string; room_code?: string; online_count?: number; online?: boolean }
export interface RoleBinding { role_id?: string; default_role_id?: string }
export interface Summary { role?: { binding: RoleBinding; name?: string }; room?: RoomState; recognition?: { enabled: boolean; count: number }; errors?: string[] }
export function roleSummary(role: Summary['role']) {
  if (!role) return t('角色获取失败，请刷新');
  if (!role.binding.role_id && !role.binding.default_role_id) return t('未配置默认角色');
  if (!role.name) return t('角色不可用，请重新选择');
  return role.binding.role_id ? t('当前：{p0}', { p0: role.name }) : t('{p0}（默认）', { p0: role.name });
}
export function roomSummary(room?: RoomState) {
  if (!room) return t('状态获取失败，请刷新');
  if (room.desired_state !== 'joined') return t(room.state === 'room_closed' ? '房间已解散，可重新建房或加入' : '当前未加入房间');
  if (room.state === 'waiting_device') return room.room_code;
  if (room.state === 'joined') return t('{p0} · {p1} 人在线', { p0: room.room_code || '', p1: room.online_count ?? 0 });
  const states: Record<string, string> = { assigned: '已安排加入', suspended: '设备忙，空闲后加入', connecting: '正在加入', connect_failed: '加入失败，将重试' };
  return `${room.room_code || ''} · ${t(states[room.state] || '正在同步')}`;
}
export function deviceRows(device: Device): [string, string][] {
  return [['设备名称', device.device_name || t('未命名设备')], ['设备 ID', device.device_id], ['MAC', device.mac], ['芯片型号', device.hardware?.chip_model], ['开发板型号', device.hardware?.board_model], ['固件版本', device.firmware_version], ['绑定时间', device.bind_time]].map(([key, value]) => [t(key || ''), value || t('未上报')]);
}
function codecs(value: unknown) {
  if (value == null) return t('未上报');
  const names: Record<string, string> = { opus: 'Opus', aac: 'AAC', none: t('无'), amr: 'AMR', amr_nb: 'AMR-NB', amr_wb: 'AMR-WB', pcm: 'PCM', alaw: 'G.711 A-law', g711a: 'G.711 A-law', h264: 'H.264', h265: 'H.265', mjpeg: 'MJPEG' };
  const list: unknown[] = Array.isArray(value) ? value : String(value).split(/[,/;|\s]+/);
  return list.filter(Boolean).map(v => names[String(v).toLowerCase()] || String(v)).join(' / ') || t('无');
}
export function mediaRows(device: Device, scene: string): [string, string][] | undefined {
  const p = device.profiles?.[scene]; if (!p || !Object.keys(p).length) return;
  const display = (key: string, format: (value: unknown) => string = String) => p[key] == null ? t('未上报') : format(p[key]);
  const video = (key: string) => p.no_video === true ? t('无') : codecs(p[key]);
  return [['上行音频', codecs(p.up_audio_mt)], ['上行视频', video('up_video_mt')], ['下行音频（首选顺序）', codecs(p.down_audio_mt)], ['下行视频（首选顺序）', video('down_video_mt')],
    ...(scene === 'stream' ? [['上行音频 Stream ID', display('up_audio_streamid')], ['上行视频 Stream ID', display('up_video_streamid')], ['下行音频 Stream ID', display('down_audio_streamid')], ['下行视频 Stream ID', display('down_video_streamid')]] : []),
    ['音频采样率', display('audio_rate', v => `${v} Hz`)], ['音频声道数', display('audio_channels')], ['摄像头旋转', display('camera_rotation', v => t('{p0}°（顺时针）', { p0: String(v) }))],
    ...(scene === 'voip' ? [['下行视频方向', display('down_video_rotation', v => t(({ 0: '默认', 1: '正向画面', 2: '保留旋转画面' } as Record<string, string>)[String(v)] || '未上报'))]] : []),
    ['水平镜像', display('hor_mirror', v => t(v ? '是' : '否'))], ['垂直镜像', display('vert_mirror', v => t(v ? '是' : '否'))], ['画面比例', display('aspect_ratio')],
    ['缩放方式', display('object_fit', v => t(({ fill: '拉伸填充', contain: '保持比例，完整显示', cover: '保持比例，裁剪填充' } as Record<string, string>)[String(v)] || String(v)))],
  ].map(([key, value]) => [t(key), value]);
}
