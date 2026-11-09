import Taro from '@tarojs/taro';
import { withDeadline } from '../domain/deadline';
import { decideMedia, type Device, type MediaIntent, type PlatformCapabilities } from '../domain/devices';
import { request, token, ApiError } from '../services/api';
import type { DeviceAuthorization } from './media';
import { buildVideoUIConfig, cachedProfile, incomingDeviceID, updateDeviceVideoProfileCache, downVideoRotationOption } from '../generated/voip-video-profile';
import { buildVoipUIConfig, CALL_DIRECTION, phoneDisplaySnapshot } from '../generated/voip-ui-config';
import { parseIncomingQuery } from '../generated/voip-incoming-query';
export type { DeviceAuthorization } from './media';

interface VoipPlugin {
  CALL_PAGE_PATH: string;
  callDevice(options: object): Promise<{ roomId: string }>;
  setUIConfig(config: object): void;
  getPluginEnterOptions?(): object;
  getPluginOnloadOptions?(): object;
  onVoipEvent(callback: (event: { eventName: string; roomId?: string }) => void): void;
}
declare function requirePlugin(name: string): { default: VoipPlugin };
declare const wx: {
  getDeviceVoIPList(options: { success(value: { list: { sn: string; status: number }[] }): void; fail(error: unknown): void }): void;
  requestDeviceVoIP(options: { sn: string; snTicket: string; modelId: string; deviceName: string; success(): void; fail(error: { errCode?: number }): void }): void;
};
let plugin: VoipPlugin | undefined;
let currentCall: { deviceId: string; roomId: string; session: string } | undefined;
let incomingTimer: ReturnType<typeof setTimeout> | undefined;
let calling = false;
const appId = () => Taro.getAccountInfoSync().miniProgram.appId;
function incoming(options?: object) {
  if (!plugin || !options) return;
  const query = parseIncomingQuery(options, 'taro', () => {});
  const ui = buildVideoUIConfig(query, cachedProfile(incomingDeviceID(query)));
  if (!Object.keys(ui).length) return;
  plugin.setUIConfig(buildVoipUIConfig(CALL_DIRECTION.DEVICE_TO_MINI_PROGRAM, ui, phoneDisplaySnapshot(undefined).recommendedContainAspectRatio));
}
export function refreshIncomingMedia() { try { incoming(plugin?.getPluginEnterOptions?.()); } catch { /* Keep plugin defaults if no incoming profile is available. */ } }
export function initializeMedia() {
  if (plugin || Taro.getDeviceInfo().platform === 'devtools') return;
  try {
    plugin = requirePlugin('wmpf-voip').default;
    refreshIncomingMedia();
    plugin.onVoipEvent(event => {
      if (event.eventName === 'callPageOnShow') {
        try { incoming(plugin?.getPluginOnloadOptions?.()); } catch { /* Keep defaults. */ }
        clearTimeout(incomingTimer);
        incomingTimer = setTimeout(() => { try { incoming(plugin?.getPluginOnloadOptions?.()); } catch { /* Keep defaults. */ } }, 0);
        return;
      }
      const localEnd = ['cancelVoip', 'abortVoip', 'hangUpVoip', 'joinFailCaller'].includes(event.eventName);
      if (!localEnd && !['rejectVoip', 'busyVoip', 'endVoip', 'finishVoip'].includes(event.eventName)) return;
      clearTimeout(incomingTimer);
      if (!currentCall || event.roomId !== currentCall.roomId) return;
      const call = currentCall; currentCall = undefined;
      if (localEnd && call.session === token()) {
        void request('/v1/voip/user/cancel', 'POST', { device_id: call.deviceId, wx_room_id: call.roomId })
          .catch(() => Taro.showToast({ title: '结束通话通知未送达，请检查设备状态', icon: 'none' }));
      }
    });
  } catch { plugin = undefined; }
}
export function cacheDeviceMedia(devices: Device[]) { updateDeviceVideoProfileCache(devices); }
export function mediaCapabilities(): PlatformCapabilities {
  return { platform: 'weapp', playback: true, authorization: typeof wx.getDeviceVoIPList === 'function' && typeof wx.requestDeviceVoIP === 'function', wechatCall: !!plugin?.callDevice && !!plugin.CALL_PAGE_PATH };
}
async function wechatLogin() {
  const login = await Taro.login({ timeout: 10000 });
  if (!login.code) throw new ApiError(-1, '微信登录失败，请重试');
  const result = await request<{ wx_user_openid: string }>('/v1/voip/user/wechat-mini-login', 'POST', { code: login.code, wx_app_id: appId() });
  if (!result?.wx_user_openid) throw new ApiError(-1, '微信身份获取失败，请重试');
  return result.wx_user_openid;
}
export async function refreshAuthorizations(devices: Device[]): Promise<Record<string, DeviceAuthorization>> {
  const result: Record<string, DeviceAuthorization> = Object.fromEntries(devices.map(d => [d.device_id, { state: 'unknown', serverSynced: false }]));
  if (!mediaCapabilities().authorization) return result;
  try {
    const native = await withDeadline<{ list: { sn: string; status: number }[] }>((resolve, reject) => wx.getDeviceVoIPList({ success: resolve, fail: reject }), 10000, '微信授权状态读取超时，请重试');
    const list = new Map((native.list || []).map(item => [item.sn, item.status]));
    for (const device of devices) result[device.device_id].state = list.has(device.device_id) ? list.get(device.device_id) === 1 ? 'authorized' : 'disabled' : 'missing';
  } catch { return result; }
  try {
    await wechatLogin();
    const server = await request<{ list: { device_id: string; authorized_device_name?: string }[] }>('/v1/voip/user/auth-list', 'GET', { wx_app_id: appId() });
    for (const item of server.list || []) if (result[item.device_id]) { result[item.device_id].serverSynced = true; result[item.device_id].authorizedName = item.authorized_device_name; }
  } catch { /* Read failure must never be interpreted as revocation. */ }
  return result;
}
async function authorize(device: Device) {
  if (!device.device_name.trim()) throw new ApiError(-1, '请先在更多菜单中设置设备名称');
  const ticket = await request<{ sn_ticket: string; device_name: string }>('/v1/voip/user/sn-ticket', 'POST', { device_id: device.device_id, wx_app_id: appId() });
  if (!ticket.sn_ticket || !ticket.device_name?.trim()) throw new ApiError(-1, '授权票据不可用，请刷新设备后重试');
  let created = true;
  await withDeadline<void>((resolve, reject) => wx.requestDeviceVoIP({
    sn: device.device_id, snTicket: ticket.sn_ticket, modelId: WX_MODEL_ID, deviceName: ticket.device_name,
    success: resolve, fail: error => { if (error.errCode === 10001) { created = false; resolve(); } else reject(new ApiError(-1, '微信授权未完成，请重试')); },
  }), 120000, '微信授权等待超时，请重新操作');
  const openId = await wechatLogin();
  await request('/v1/voip/user/report-auth', 'POST', { device_id: device.device_id, wx_app_id: appId(), wx_model_id: WX_MODEL_ID, wx_open_id: openId, device_name: ticket.device_name, authorization_created: created });
}
export async function performMedia(intent: MediaIntent, device: Device, authorization: DeviceAuthorization): Promise<void> {
  if (intent === 'wechat-call' && Taro.getDeviceInfo().platform === 'devtools') throw new ApiError(-1, '开发者工具不支持微信通话，请在手机微信中测试');
  let auth = authorization;
  let decision = decideMedia(intent, device, auth.state, mediaCapabilities());
  if (decision.action === 'refresh-authorization') {
    auth = (await refreshAuthorizations([device]))[device.device_id];
    decision = decideMedia(intent, device, auth.state, mediaCapabilities());
  }
  if (decision.action === 'refresh-authorization') throw new ApiError(-1, '无法确认微信授权状态，请稍后重试');
  if (decision.action === 'blocked') throw new ApiError(-1, decision.message);
  if (decision.action === 'playback') { await Taro.navigateTo({ url: '/pages/playback/index?device_id=' + encodeURIComponent(device.device_id) }); return; }
  if (decision.action === 'open-settings') { await Taro.openSetting(); return; }
  if (decision.action === 'authorize') {
    await authorize(device);
    if (intent !== 'wechat-call') return;
    auth = (await refreshAuthorizations([device]))[device.device_id];
    decision = decideMedia(intent, device, auth.state, mediaCapabilities());
  }
  if (decision.action !== 'wechat-call') throw new ApiError(-1, '请完成微信授权后重试');
  if (calling || currentCall) throw new ApiError(-1, '已有通话正在进行');
  calling = true;
  try {
    await wechatLogin();
    if (!auth.serverSynced) {
      await authorize(device);
      auth = (await refreshAuthorizations([device]))[device.device_id];
      if (!auth.serverSynced || auth.state !== 'authorized') throw new ApiError(-1, '微信授权尚未同步，请刷新后重试');
    }
    if (!plugin) throw new ApiError(-1, '微信通话插件尚未就绪');
    if (decision.roomType === 'video') {
      const ui = buildVideoUIConfig(decision.profile, undefined);
      plugin.setUIConfig(buildVoipUIConfig(CALL_DIRECTION.MINI_PROGRAM_TO_DEVICE, ui, phoneDisplaySnapshot(undefined).recommendedContainAspectRatio));
    }
    const session = token();
    const { roomId } = await plugin.callDevice({ sn: device.device_id, modelId: WX_MODEL_ID, roomType: decision.roomType,
      enableCallerCamera: decision.callerCamera, enableListenerCamera: decision.listenerCamera, nickName: '微信用户',
      deviceName: auth.authorizedName || device.device_name, isCloud: true, payload: `${Date.now()}-${Math.random().toString(16).slice(2)}`,
      ...downVideoRotationOption(decision.profile.down_video_rotation),
    });
    if (!roomId) throw new ApiError(-1, '创建通话失败，请重试');
    currentCall = { deviceId: device.device_id, roomId, session };
    try { await Taro.navigateTo({ url: plugin.CALL_PAGE_PATH }); }
    catch { currentCall = undefined; await request('/v1/voip/user/cancel', 'POST', { device_id: device.device_id, wx_room_id: roomId }); throw new ApiError(-1, '通话页面打开失败，请重试'); }
  } finally { calling = false; }
}
