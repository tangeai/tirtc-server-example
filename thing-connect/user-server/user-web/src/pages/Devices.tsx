import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Account, Dropdown, NavigationLinks } from '../components/Account';
import { DeviceIcon } from '../components/DeviceIcon';
import { LanguageSwitcher } from '../components/LanguageSwitcher';
import { Dialog } from '../components/Dialog';
import { Recognition } from '../components/Recognition';
import { ApiError, errorText, request } from '../lib/api';
import { deviceRows, mediaRows, roleSummary, roomSummary, type Device, type RoleBinding, type RoomState, type Summary } from '../lib/devices';
import { useLanguage } from '../lib/i18n';
import { markAway, restoreSnapshot, saveSnapshot, snapshotKey } from '../lib/snapshot';

interface Snapshot { devices: Device[]; summaries: Record<string, Summary> }
type Modal = { kind: 'rename' | 'info' | 'unbind' | 'recognition'; device: Device };
export default function Devices() {
  const { t } = useLanguage();
  const [data, setData] = useState<Snapshot>({ devices: [], summaries: {} }), [loading, setLoading] = useState(true), [error, setError] = useState(''), [restored, setRestored] = useState(false);
  const [modal, setModal] = useState<Modal>(), [adding, setAdding] = useState(false);
  const cache = useRef(''), sequence = useRef(0), current = useRef(data), addingRef = useRef(false);
  current.current = data;
  function update(fn: (state: Snapshot) => Snapshot) { setData(previous => { const next = fn(previous); current.current = next; if (cache.current) saveSnapshot(cache.current, next); return next; }); }
  async function refresh() {
    const rev = ++sequence.current; setLoading(true); setError(''); setRestored(false);
    try {
      const result = await request<Device[]>('/v1/user/device/list');
      if (rev !== sequence.current) return;
      // Keep only presentation fields; API credentials never enter tab storage.
      const devices = result.map(({ device_id, device_name, online, status, mac, hardware, firmware_version, bind_time, profiles }) => ({ device_id, device_name, online, status, mac, hardware, firmware_version, bind_time, profiles }));
      update(() => ({ devices, summaries: {} }));
      let cursor = 0;
      const roles = new Map<string, Promise<{ name: string }>>();
      async function worker() {
        while (cursor < devices.length && rev === sequence.current) {
          const device = devices[cursor++], id = encodeURIComponent(device.device_id);
          const summary: Summary = {};
          await Promise.allSettled([
            request<RoomState>('/v1/call/group/web/device/' + id).then(room => { summary.room = room; }),
            request<{ policy: { enabled: boolean }; schemes: unknown[] }>('/v1/recognition/devices/' + id + '/config').then(value => { summary.recognition = { enabled: value.policy.enabled, count: value.schemes.length }; }),
            (async () => {
              const binding = await request<RoleBinding>('/v1/ai/device/' + id + '/role');
              summary.role = { binding };
              if (!binding.role_id && !binding.default_role_id) return;
              const path = binding.role_id ? '/v1/ai/roles/' + encodeURIComponent(binding.role_id) : '/v1/ai/roles/default';
              if (!roles.has(path)) roles.set(path, request<{ name: string }>(path));
              try { summary.role.name = (await roles.get(path))?.name; }
              catch (e) { if (!(e instanceof ApiError) || ![403, 404, 40300, 40400].includes(e.code)) summary.role = undefined; }
            })(),
          ]);
          if (rev === sequence.current) update(state => ({ ...state, summaries: { ...state.summaries, [device.device_id]: summary }, devices: state.devices.map(d => d.device_id === device.device_id && typeof summary.room?.online === 'boolean' ? { ...d, online: summary.room.online } : d) }));
        }
      }
      await Promise.all(Array.from({ length: Math.min(4, devices.length) }, worker));
    } catch (e) { if (rev === sequence.current) setError(errorText(e)); }
    finally { if (rev === sequence.current) setLoading(false); }
  }
  useEffect(() => {
    let alive = true;
    const away = () => { if (cache.current) { saveSnapshot(cache.current, current.current); markAway(cache.current); } };
    window.addEventListener('pagehide', away);
    void (async () => {
      try {
        cache.current = await snapshotKey('devices');
        const saved = restoreSnapshot<Snapshot>(cache.current);
        if (!alive) return;
        if (saved && Array.isArray(saved.devices) && saved.devices.every(d => typeof d.device_id === 'string') && saved.summaries) { setData(saved); setLoading(false); setRestored(true); return; }
      } catch { /* Storage availability does not affect loading. */ }
      if (alive) await refresh();
    })();
    return () => { alive = false; sequence.current++; window.removeEventListener('pagehide', away); };
  }, []);
  async function add() {
    if (addingRef.current) return; addingRef.current = true; setAdding(true);
    try { const quota = await request<{ quota: number }>('/v1/user/quota'); if (quota.quota <= 0) setError('设备额度已用尽，请联系管理员申请扩容'); else location.assign('/bind'); }
    catch (e) { setError(errorText(e)); } finally { addingRef.current = false; setAdding(false); }
  }
  const close = () => setModal(undefined);
  const href = (page: string, device: Device) => page + '?device_id=' + encodeURIComponent(device.device_id);
  return <><header className="topbar"><span className="brand">◉ {t('小钛控制台')}</span><div className="header-actions"><NavigationLinks/><LanguageSwitcher/><Account/></div></header>
    <main className="device-layout"><div className="row"><h1>{t('我的设备')} <small>({data.devices.length})</small></h1><button className="device-refresh" disabled={loading} onClick={() => void refresh()}><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><path d="M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-1l2 3M4 15l2 3a7 7 0 0 0 12-1"/></svg>{t('刷新')}</button></div>
      {loading && <p role="status" className="muted">{t('正在加载设备…')}</p>}{error && <p className="error" role="alert">{t(error)}</p>}{restored && <p className="muted">{t('显示上次查看的设备状态，点击刷新获取最新状态。')}</p>}
      <div className="device-grid">{data.devices.map(device => { const summary = data.summaries[device.device_id]; return <article className="card device-card" key={device.device_id} data-device-id={device.device_id}>
        <div className="row"><div className="device-title"><span className="device-symbol"><DeviceIcon name="monitor"/></span><h2>{device.device_name || t('未命名设备')}</h2></div><Dropdown label={t('更多 ▾')}><button onClick={() => setModal({ kind: 'rename', device })}>{t('修改名称')}</button><button onClick={() => setModal({ kind: 'info', device })}>{t('设备信息')}</button><button className="danger" onClick={() => setModal({ kind: 'unbind', device })}>{t('解绑设备')}</button></Dropdown></div>
        <div className="row device-meta"><p className="muted">ID: {device.device_id}</p><span className={device.online ? 'success' : 'muted'}>{t(device.online ? '● 在线' : '○ 离线')}</span></div>
        <div className="device-summaries"><a href={href('/agent', device)}><strong>{t('AI 角色')} <span>›</span></strong><p>{summary ? roleSummary(summary.role) : t('正在查询…')}</p></a><button onClick={() => setModal({ kind: 'recognition', device })}><strong>{t('AI识别')} <span>›</span></strong><p>{summary ? summary.recognition ? t(summary.recognition.enabled ? '已开启 · {p0}项' : '已关闭', { p0: summary.recognition.count }) : t('状态获取失败，请刷新') : t('正在查询…')}</p></button><a href={href('/room', device)}><strong>{t('多人对讲')} <span>›</span></strong><p>{summary ? roomSummary(summary.room) : t('正在查询…')}</p></a></div>
        <nav className="device-actions"><a href={href('/player', device)}><DeviceIcon name="monitor"/><span>{t('实时')}</span></a><a href={href('/contacts', device)}><DeviceIcon name="contacts"/><span>{t('联系人')}</span></a><a href={href('/agent', device)}><DeviceIcon name="role"/><span>{t('AI 角色')}</span></a><a href={href('/room', device)}><DeviceIcon name="room"/><span>{t('多人对讲')}</span>{summary?.room?.desired_state === 'joined' && <span aria-label={t('有对讲关系')}> ●</span>}</a></nav>
      </article>; })}</div>
      {!loading && !data.devices.length && !error && <section className="card"><h2>{t('还没有设备')}</h2><p className="muted">{t('点击"添加设备"，输入验证码完成绑定')}</p></section>}
    <div className="device-list-footer"><button className="primary add-device" disabled={adding} onClick={() => void add()}>＋ {t('添加设备')}</button></div></main>
    {modal?.kind === 'info' && <DeviceInfo device={modal.device} close={close}/>}
    {modal && ['rename', 'unbind'].includes(modal.kind) && <DeviceMutation key={modal.kind + modal.device.device_id} modal={modal} close={close} done={(name?: string) => {
      update(state => ({ ...state, devices: name === undefined ? state.devices.filter(d => d.device_id !== modal.device.device_id) : state.devices.map(d => d.device_id === modal.device.device_id ? { ...d, device_name: name } : d) }));
      setModal(current => current === modal ? undefined : current);
      void refresh();
    }}/>}
    {modal?.kind === 'recognition' && <Recognition key={modal.device.device_id} device={modal.device.device_id} close={() => setModal(current => current === modal ? undefined : current)} saved={(enabled, count) => { update(state => ({ ...state, summaries: { ...state.summaries, [modal.device.device_id]: { ...state.summaries[modal.device.device_id], recognition: { enabled, count } } } })); void refresh(); }}/>}
  </>;
}
function DeviceMutation({ modal, close, done }: { modal: Modal; close(): void; done(name?: string): void }) {
  const { t } = useLanguage(); const rename = modal.kind === 'rename';
  const [value, setValue] = useState(modal.device.device_name || ''), [busy, setBusy] = useState(false), [error, setError] = useState('');
  const alive = useRef(true), pending = useRef(false);
  useEffect(() => () => { alive.current = false; }, []);
  async function submit(event: FormEvent) {
    event.preventDefault(); if (pending.current) return;
    const name = value.trim();
    if (rename && (!name || Array.from(name).length > 13)) { setError(!name ? '请输入设备名称' : '设备名称最多 13 个字符'); return; }
    pending.current = true; setBusy(true); setError('');
    try {
      await request(rename ? '/v1/user/device/name' : '/v1/user/device/reset', { method: rename ? 'PUT' : 'DELETE', body: rename ? { device_id: modal.device.device_id, device_name: name } : { device_id: modal.device.device_id } });
      done(rename ? name : undefined);
    } catch (e) { if (alive.current) setError(errorText(e)); }
    finally { pending.current = false; if (alive.current) setBusy(false); }
  }
  return <Dialog title={rename ? '设置设备名称' : '确认解绑设备？'} onClose={close}><p className="muted">{modal.device.device_id}</p><form onSubmit={event => void submit(event)}>
    {rename ? <><label>{t('设备名称')}<input autoFocus disabled={busy} value={value} onChange={e => setValue(e.target.value)}/></label><p className="muted">{Array.from(value).length} / 13</p><p className="muted">{t('最多 13 个字符，用于设备列表和微信设备来电显示。')}</p><p className="muted">{t('如果微信已保存旧的授权名称，需要清除原授权并重新授权后，新名称才会在微信端生效。')}</p></> : <p>{t('解绑后设备会退出当前房间，并从我的设备中移除。再次使用需要重新绑定。')}</p>}
    {error && <p className="error" role="alert">{t(error)}</p>}<div className="row"><button type="button" onClick={close}>{t('取消')}</button><button disabled={busy} className={rename ? 'primary' : 'danger'}>{t(busy ? '保存中…' : rename ? '保存名称' : '确认解绑')}</button></div>
  </form></Dialog>;
}
function Rows({ rows }: { rows: [string, string][] }) { return <dl className="detail-list">{rows.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>; }
function DeviceInfo({ device, close }: { device: Device; close(): void }) {
  const { t } = useLanguage(); const [scene, setScene] = useState('stream'); const rows = mediaRows(device, scene);
  return <Dialog title="设备信息" onClose={close}><Rows rows={deviceRows(device)}/><h2>{t('媒体能力')}</h2><nav className="tabs" aria-label={t('媒体场景')}>{Object.entries({ stream: '实时查看', call: '设备通话', voip: '微信 VoIP' }).map(([key, label]) => <button key={key} aria-pressed={scene === key} onClick={() => setScene(key)}>{t(label)}</button>)}</nav>{rows ? <Rows rows={rows}/> : <p className="muted">{t('该场景能力未上报')}</p>}</Dialog>;
}
