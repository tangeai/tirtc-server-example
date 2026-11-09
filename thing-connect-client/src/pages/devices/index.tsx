import Taro, { useDidShow } from '@tarojs/taro';
import { Button, Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { DeviceInfo } from '../../components/device-info';
import { Action, Field, Notice, RobotIcon, Sheet } from '../../components/ui';
import { request, token, errorText } from '../../services/api';
import { deviceRevision } from '../../services/device-events';
import type { Device, MediaIntent } from '../../domain/devices';
import { cacheDeviceMedia, mediaCapabilities, performMedia, refreshAuthorizations, type DeviceAuthorization } from '../../platform/media';

const unknownAuth: DeviceAuthorization = { state: 'unknown', serverSynced: false };
function DeviceMenuItem({ title, description, icon, badge, danger, onClick }: { title: string; description: string; icon: string; badge?: string; danger?: boolean; onClick(): void }) {
  return <Button className={'device-menu-item' + (danger ? ' is-danger' : '')} onClick={onClick}><View className={'device-menu-icon menu-icon-' + icon} ariaHidden><View className='menu-icon-shape' /><Text className='menu-icon-mark'>{icon === 'auth' ? '✓' : icon === 'info' ? 'i' : ''}</Text></View><View className='device-menu-copy'><View className='device-menu-heading'><Text className='device-menu-title'>{title}</Text>{badge && <Text className='device-menu-badge'>{badge}</Text>}</View><Text className='device-menu-description'>{description}</Text></View>{!danger && <Text className='device-menu-chevron'>›</Text>}</Button>;
}
export default function Devices() {
  const [devices, setDevices] = useState<Device[]>([]), [loading, setLoading] = useState(false), [initialized, setInitialized] = useState(false), [error, setError] = useState('');
  const [authorizations, setAuthorizations] = useState<Record<string, DeviceAuthorization>>({});
  const [menu, setMenu] = useState<Device>(), [editing, setEditing] = useState<Device>(), [name, setName] = useState(''), [info, setInfo] = useState<Device>();
  const [busy, setBusy] = useState(false), [editError, setEditError] = useState('');
  const alive = useRef(true), reading = useRef(false), writing = useRef(false), revision = useRef(0), loadedRevision = useRef(-1);
  const capabilities = mediaCapabilities();
  const overlayOpen = !!(menu || editing || info);
  useEffect(() => {
    if (!overlayOpen) return;
    let closed = false;
    void Taro.hideTabBar({ animation: false }).then(() => {
      if (closed) void Taro.showTabBar({ animation: false }).catch(() => {});
    }).catch(() => {});
    return () => { closed = true; void Taro.showTabBar({ animation: false }).catch(() => {}); };
  }, [overlayOpen]);
  useEffect(() => { alive.current = true; return () => { alive.current = false; revision.current++; }; }, []);
  useDidShow(() => {
    if (!token()) { void Taro.reLaunch({ url: '/pages/login/index' }); return; }
    if (loadedRevision.current !== deviceRevision()) void load();
  });
  async function load() {
    if (reading.current) return;
    reading.current = true; const rev = ++revision.current; const changed = deviceRevision();
    loadedRevision.current = changed;
    setLoading(true); setError('');
    try {
      const rows = (await request<Device[]>('/v1/user/device/list')).filter(d => d.status === 1).sort((a, b) => (b.bind_time || '').localeCompare(a.bind_time || ''));
      if (!alive.current || rev !== revision.current) return;
      setDevices(rows); cacheDeviceMedia(rows);
      const auth = await refreshAuthorizations(rows);
      if (alive.current && rev === revision.current) setAuthorizations(auth);
    } catch (e) { if (alive.current && rev === revision.current) setError(errorText(e)); }
    finally { reading.current = false; if (alive.current && rev === revision.current) { setLoading(false); setInitialized(true); } }
  }
  async function mutate(operation: () => Promise<void>, inEditor = false) {
    if (writing.current) return;
    writing.current = true; setBusy(true); (inEditor ? setEditError : setError)('');
    try { await operation(); }
    catch (e) { if (alive.current) (inEditor ? setEditError : setError)(errorText(e)); }
    finally { writing.current = false; if (alive.current) setBusy(false); }
  }
  async function media(intent: MediaIntent, device: Device) {
    await mutate(async () => {
      await performMedia(intent, device, authorizations[device.device_id] || unknownAuth);
      if (intent === 'authorize' && alive.current) {
        const auth = await refreshAuthorizations([device]);
        if (alive.current) setAuthorizations(current => ({ ...current, ...auth }));
      }
    });
  }
  async function rename() {
    if (!editing) return;
    const value = name.trim();
    if (!value || Array.from(value).length > 13) { setEditError('请输入 1–13 个字符的设备名称'); return; }
    const device = editing;
    await mutate(async () => {
      await request('/v1/user/device/name', 'PUT', { device_id: device.device_id, device_name: value });
      if (!alive.current) return;
      revision.current++; setLoading(false);
      setDevices(rows => rows.map(row => row.device_id === device.device_id ? { ...row, device_name: value } : row)); setEditing(undefined);
      const snapshot = authorizations[device.device_id]?.authorizedName;
      if (snapshot && snapshot !== value) await Taro.showModal({ title: '名称已保存', content: '微信来电名称仍使用上次授权时的名称。请在微信中清理本小程序的授权记录后重新授权，更新来电名称。', showCancel: false });
    }, true);
  }
  async function unbind(device: Device) {
    const result = await Taro.showModal({ title: '解绑设备', content: `解绑后无法查看或呼叫“${device.device_name || device.device_id}”，微信通话授权也会清除。是否继续？`, confirmText: '解绑', confirmColor: '#fa5151' });
    if (!result.confirm || !alive.current) return;
    await mutate(async () => { await request('/v1/user/device/reset', 'DELETE', { device_id: device.device_id }); if (alive.current) { revision.current++; setLoading(false); setDevices(rows => rows.filter(row => row.device_id !== device.device_id)); } });
  }
  const authLabel = (device: Device) => {
    const auth = authorizations[device.device_id] || unknownAuth;
    return auth.state === 'authorized' ? auth.serverSynced ? '已授权' : '待同步' : auth.state === 'disabled' ? '提醒已关闭' : auth.state === 'missing' ? '待授权' : '待确认';
  };
  const needsAuthorization = (device: Device) => ['missing', 'disabled'].includes(authorizations[device.device_id]?.state || 'unknown');
  const callLabel = (device: Device) => authorizations[device.device_id]?.state === 'missing' ? '授权微信通话' : authorizations[device.device_id]?.state === 'disabled' ? '开启通话提醒' : '微信通话';
  return <View className='page'>
      <View className='row between' style={{ marginBottom: '24px' }}><Text className='title'>我的设备 <Text className='muted'>{devices.length}</Text></Text><View className='row'><Action disabled={loading || busy} onClick={() => void load()}>刷新</Action><Action primary onClick={() => void Taro.navigateTo({ url: '/pages/bind/index' })}>＋ 添加设备</Action></View></View>
      <Notice error>{error}</Notice>{loading && <Notice>正在更新设备…</Notice>}
      {initialized && !devices.length && <View className='card empty'>{error ? '设备列表暂时无法加载，请刷新重试' : '添加第一台设备，开始使用小钛'}</View>}
      {devices.map(device => <View className='card' key={device.device_id}><View className='device-head'><View className={'device-symbol ' + (device.online ? 'device-symbol-online' : 'device-symbol-offline')}><RobotIcon /><Text className='device-symbol-status'>{device.online ? '在线' : '离线'}</Text></View><View className='grow device-summary'><View className='device-title-row'><Text className='device-name'>{device.device_name || '未命名设备'}</Text></View><Text className='device-id'>ID: {device.device_id}</Text></View><Action disabled={busy} onClick={() => setMenu(device)}>⋯</Action></View>
        <View className='device-actions device-primary-actions'>{capabilities.platform === 'weapp' && <Action primary disabled={busy || loading || (!device.online && !needsAuthorization(device))} onClick={() => void media(needsAuthorization(device) ? 'authorize' : 'wechat-call', device)}><View className='device-primary-label'><View className={'device-action-icon ' + (needsAuthorization(device) ? 'device-action-shield' : 'device-action-phone')} ariaHidden />{callLabel(device)}</View></Action>}<Action disabled={busy || !device.online || !capabilities.playback} onClick={() => void media('playback', device)}><View className='device-primary-label'><View className='device-action-icon device-action-play' ariaHidden />实时查看</View></Action></View>
        <View className='device-feature-links'>{([['roles', 'AI 角色'], ['recognition', 'AI 识别'], ['contacts', '联系人']] as const).map(([page, title]) => <Action key={page} onClick={() => void Taro.navigateTo({ url: '/pages/' + page + '/index?device_id=' + encodeURIComponent(device.device_id) })}>{title}<Text className='device-link-arrow'>›</Text></Action>)}</View>
      </View>)}
    {menu && <View className='mask device-more-mask' onClick={() => setMenu(undefined)}><View className='device-more-sheet' onClick={e => e.stopPropagation()}><View className='device-more-header'><Text className='device-more-title'>更多操作</Text><Text className='device-more-subtitle'>{menu.device_name || '未命名设备'}</Text></View><View className='device-more-scroll'>
      <View className='device-menu-list'>
        {capabilities.platform === 'weapp' && <DeviceMenuItem icon='auth' title='微信通话授权' badge={authLabel(menu)} description='管理微信与设备的音视频通话授权' onClick={() => { const device = menu; setMenu(undefined); void media('authorize', device); }} />}
        <DeviceMenuItem icon='group' title='多人对讲' description='创建或加入房间，与多台设备语音对讲' onClick={() => { const device = menu; setMenu(undefined); void Taro.navigateTo({ url: '/pages/room/index?device_id=' + encodeURIComponent(device.device_id) }); }} />
        <DeviceMenuItem icon='history' title='AI 对话记录' description='查看设备与 AI 的历史文字对话' onClick={() => { const device = menu; setMenu(undefined); void Taro.navigateTo({ url: '/pages/history/index?device_id=' + encodeURIComponent(device.device_id) }); }} />
        <DeviceMenuItem icon='edit' title='修改设备名称' description='为设备设置方便辨认的名称' onClick={() => { setEditing(menu); setName(menu.device_name); setEditError(''); setMenu(undefined); }} />
        <DeviceMenuItem icon='info' title='设备信息' description='查看设备 ID 及播放、通话能力' onClick={() => { setInfo(menu); setMenu(undefined); }} />
      </View>
      <View className='device-menu-danger'><DeviceMenuItem icon='remove' danger title='解绑设备' description='移除设备，同时清除微信通话授权' onClick={() => { const device = menu; setMenu(undefined); void unbind(device); }} /></View>
    </View><View className='device-more-footer'><Button className='device-more-cancel' onClick={() => setMenu(undefined)}>取消</Button></View></View></View>}
    {editing && <Sheet title='修改设备名称' busy={busy} onClose={() => setEditing(undefined)}><Field label='设备名称' value={name} onChange={setName} disabled={busy} /><Text className='muted'>最多 13 个字符，用于设备列表与微信通话授权。</Text><Notice error>{editError}</Notice><View className='wide'><Action primary disabled={busy} onClick={() => void rename()}>保存名称</Action></View></Sheet>}
    {info && <DeviceInfo key={info.device_id} device={info} onClose={() => setInfo(undefined)} />}
  </View>;
}
