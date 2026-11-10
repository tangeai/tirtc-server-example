import Taro, { useDidShow, usePullDownRefresh } from '@tarojs/taro';
import { Button, Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { DeviceInfo } from '../../components/device-info';
import { Action, Field, Notice, RobotIcon, Sheet } from '../../components/ui';
import { request, token, errorText } from '../../services/api';
import { readDevices } from '../../services/device-directory';
import { deviceRevision, devicesChanged } from '../../services/device-events';
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
  const authRevision = useRef(0), authActions = useRef<Record<string, number>>({});
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
  useEffect(() => { alive.current = true; return () => { alive.current = false; revision.current++; authRevision.current++; }; }, []);
  useDidShow(() => {
    if (!token()) { void Taro.reLaunch({ url: '/pages/login/index' }); return; }
    if (loadedRevision.current !== deviceRevision()) void load();
  });
  usePullDownRefresh(() => {
    if (writing.current || overlayOpen) { Taro.stopPullDownRefresh(); return; }
    void load();
  });
  async function load() {
    if (reading.current) return;
    reading.current = true; const rev = ++revision.current; const changed = deviceRevision();
    loadedRevision.current = changed;
    setLoading(true); setError('');
    try {
      const rows = [...await readDevices(true)].sort((a, b) => (b.bind_time || '').localeCompare(a.bind_time || ''));
      if (!alive.current || rev !== revision.current) return;
      setDevices(rows); cacheDeviceMedia(rows);
      void loadAuthorizations(rows);
    } catch (e) { if (alive.current && rev === revision.current) setError(errorText(e)); }
    finally { reading.current = false; if (alive.current) { Taro.stopPullDownRefresh(); if (rev === revision.current) { setLoading(false); setInitialized(true); } } }
  }
  async function loadAuthorizations(rows: Device[]) {
    const rev = ++authRevision.current, session = token(), actions = { ...authActions.current };
    // Unknown stays unknown on read failure; never infer revocation from a timeout.
    setAuthorizations({});
    try {
      const auth = await refreshAuthorizations(rows);
      if (alive.current && rev === authRevision.current && session === token()) {
        setAuthorizations(current => Object.fromEntries(rows.map(device => {
          const id = device.device_id;
          return [id, actions[id] === authActions.current[id] ? auth[id] || unknownAuth : current[id] || unknownAuth];
        })));
      }
    } catch { /* Individual actions can retry authorization checks. */ }
  }
  async function mutate(operation: () => Promise<void>, inEditor = false) {
    if (writing.current) return;
    writing.current = true; setBusy(true); (inEditor ? setEditError : setError)('');
    try { await operation(); }
    catch (e) { if (alive.current) (inEditor ? setEditError : setError)(errorText(e)); }
    finally { writing.current = false; if (alive.current) setBusy(false); }
  }
  async function media(intent: MediaIntent, device: Device) {
    if (writing.current) return;
    if (intent !== 'authorize' && !device.online) {
      void Taro.showToast({ title: '设备已离线，请检查电源和网络', icon: 'none', duration: 2500 });
      return;
    }
    const authRev = authRevision.current, session = token(), id = device.device_id;
    const action = intent === 'playback' ? authActions.current[id] : (authActions.current[id] = (authActions.current[id] || 0) + 1);
    await mutate(async () => {
      await performMedia(intent, device, authorizations[device.device_id] || unknownAuth);
      if (intent === 'authorize' && alive.current) {
        const auth = await refreshAuthorizations([device]);
        if (alive.current && authRev === authRevision.current && action === authActions.current[id] && session === token()) setAuthorizations(current => ({ ...current, ...auth }));
      }
    });
  }
  function openRename(device: Device) {
    if (writing.current) return;
    setMenu(undefined); setEditing(device); setName(device.device_name || ''); setEditError('');
  }
  async function rename() {
    if (!editing) return;
    const value = name.trim();
    if (!value || Array.from(value).length > 13) { setEditError('请输入 1–13 个字符的设备名称'); return; }
    const device = editing;
    await mutate(async () => {
      await request('/v1/user/device/name', 'PUT', { device_id: device.device_id, device_name: value });
      devicesChanged();
      if (!alive.current) return;
      revision.current++; authRevision.current++; loadedRevision.current = deviceRevision(); setLoading(false);
      setDevices(rows => rows.map(row => row.device_id === device.device_id ? { ...row, device_name: value } : row)); setEditing(undefined);
      const snapshot = authorizations[device.device_id]?.authorizedName;
      if (snapshot && snapshot !== value) await Taro.showModal({ title: '名称已保存', content: '微信来电名称仍使用上次授权时的名称。请在微信中清理本小程序的授权记录后重新授权，更新来电名称。', showCancel: false });
    }, true);
  }
  async function unbind(device: Device) {
    const result = await Taro.showModal({ title: '解绑设备', content: `解绑后无法查看或呼叫“${device.device_name || device.device_id}”，微信通话授权也会清除。是否继续？`, confirmText: '解绑', confirmColor: '#fa5151' });
    if (!result.confirm || !alive.current) return;
    await mutate(async () => { await request('/v1/user/device/reset', 'DELETE', { device_id: device.device_id }); devicesChanged(); if (alive.current) { revision.current++; authRevision.current++; loadedRevision.current = deviceRevision(); setLoading(false); setDevices(rows => rows.filter(row => row.device_id !== device.device_id)); } });
  }
  const authLabel = (device: Device) => {
    const auth = authorizations[device.device_id] || unknownAuth;
    return auth.state === 'authorized' ? auth.serverSynced ? '已授权' : '待同步' : auth.state === 'disabled' ? '提醒已关闭' : auth.state === 'missing' ? '待授权' : '待确认';
  };
  const needsAuthorization = (device: Device) => ['missing', 'disabled'].includes(authorizations[device.device_id]?.state || 'unknown');
  const callLabel = (device: Device) => authorizations[device.device_id]?.state === 'missing' ? '授权微信通话' : authorizations[device.device_id]?.state === 'disabled' ? '开启通话提醒' : (authorizations[device.device_id]?.state === 'authorized' ? '微信通话' : '确认通话授权');
  return <View className='page'>
      <View className='row between' style={{ marginBottom: '24px' }}><Text className='title'>我的设备 <Text className='muted'>{devices.length}</Text></Text><View className='row'><Action primary onClick={() => void Taro.navigateTo({ url: '/pages/bind/index' })}>＋ 添加设备</Action></View></View>
      <Notice error>{error}</Notice>{loading && <Notice>正在更新设备…</Notice>}
      {initialized && !devices.length && <View className='card empty'>{error ? '设备列表暂时无法加载，请下拉刷新重试' : '添加第一台设备，开始使用小钛'}</View>}
      {devices.map(device => <View className='card' key={device.device_id}><View className='device-head'><View className={'device-symbol ' + (device.online ? 'device-symbol-online' : 'device-symbol-offline')}><RobotIcon /><Text className='device-symbol-status'>{device.online ? '在线' : '离线'}</Text></View><View className='grow device-summary'><View className='device-title-row'>{device.device_name?.trim() ? <Text className='device-name'>{device.device_name}</Text> : <Button className='device-name-setup' disabled={busy} ariaLabel={'设置设备名称：' + device.device_id} onClick={() => openRename(device)}><Text>设置名称</Text><View className='device-name-edit-icon' ariaHidden /></Button>}</View><Text className='device-id'>ID: {device.device_id}</Text></View><Action disabled={busy} onClick={() => setMenu(device)}>⋯</Action></View>
        <View className='device-actions device-primary-actions'>{capabilities.platform === 'weapp' && <Action primary muted={!device.online && !needsAuthorization(device)} disabled={busy || loading} onClick={() => void media(needsAuthorization(device) ? 'authorize' : 'wechat-call', device)}><View className='device-primary-label'><View className={'device-action-icon ' + (needsAuthorization(device) ? 'device-action-shield' : 'device-action-phone')} ariaHidden />{callLabel(device)}</View></Action>}<Action muted={!device.online} disabled={busy || !capabilities.playback} onClick={() => void media('playback', device)}><View className='device-primary-label'><View className='device-action-icon device-action-play' ariaHidden />实时查看</View></Action></View>
        <View className='device-feature-links'>{([['roles', 'AI 角色'], ['recognition', 'AI 识别'], ['contacts', '联系人']] as const).map(([page, title]) => <Action key={page} onClick={() => void Taro.navigateTo({ url: '/pages/' + page + '/index?device_id=' + encodeURIComponent(device.device_id) })}>{title}<Text className='device-link-arrow'>›</Text></Action>)}</View>
      </View>)}
    {menu && <View className='mask device-more-mask' onClick={() => setMenu(undefined)}><View className='device-more-sheet' onClick={e => e.stopPropagation()}><View className='device-more-header'><Text className='device-more-title'>更多操作</Text><Text className='device-more-subtitle'>{menu.device_name || '未命名设备'}</Text></View><View className='device-more-scroll'>
      <View className='device-menu-list'>
        {capabilities.platform === 'weapp' && <DeviceMenuItem icon='auth' title='微信通话授权' badge={authLabel(menu)} description='管理微信与设备的音视频通话授权' onClick={() => { const device = menu; setMenu(undefined); void media('authorize', device); }} />}
        <DeviceMenuItem icon='group' title='多人对讲' description='创建或加入房间，与多台设备语音对讲' onClick={() => { const device = menu; setMenu(undefined); void Taro.navigateTo({ url: '/pages/room/index?device_id=' + encodeURIComponent(device.device_id) }); }} />
        <DeviceMenuItem icon='history' title='AI 对话记录' description='查看设备与 AI 的历史文字对话' onClick={() => { const device = menu; setMenu(undefined); void Taro.navigateTo({ url: '/pages/history/index?device_id=' + encodeURIComponent(device.device_id) }); }} />
        <DeviceMenuItem icon='edit' title='修改设备名称' description='为设备设置方便辨认的名称' onClick={() => openRename(menu)} />
        <DeviceMenuItem icon='info' title='设备信息' description='查看设备 ID 及播放、通话能力' onClick={() => { setInfo(menu); setMenu(undefined); }} />
      </View>
      <View className='device-menu-danger'><DeviceMenuItem icon='remove' danger title='解绑设备' description='移除设备，同时清除微信通话授权' onClick={() => { const device = menu; setMenu(undefined); void unbind(device); }} /></View>
    </View><View className='device-more-footer'><Button className='device-more-cancel' onClick={() => setMenu(undefined)}>取消</Button></View></View></View>}
    {editing && <Sheet title='修改设备名称' busy={busy} onClose={() => setEditing(undefined)}><Field label='设备名称' value={name} onChange={setName} disabled={busy} /><Text className='muted'>最多 13 个字符，用于设备列表与微信通话授权。</Text><Notice error>{editError}</Notice><View className='wide'><Action primary disabled={busy} onClick={() => void rename()}>保存名称</Action></View></Sheet>}
    {info && <DeviceInfo key={info.device_id} device={info} onClose={() => setInfo(undefined)} />}
  </View>;
}
