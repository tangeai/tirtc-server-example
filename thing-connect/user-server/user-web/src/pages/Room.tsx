import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Dialog } from '../components/Dialog';
import { Shell } from '../components/Shell';
import { errorText, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';
import { markAway, restoreSnapshot, saveSnapshot, snapshotKey } from '../lib/snapshot';

interface Assignment { room_id: number; assignment_version: number; desired_state: string; state: string; room_code?: string; online?: boolean; online_count?: number; password_set?: boolean }
interface Snapshot { name: string; assignment: Assignment; verified: boolean }
const states: Record<string, string> = { assigned: '已安排加入', waiting_device: '设备离线', suspended: '设备忙，空闲后加入', connecting: '正在加入', joined: '已加入', connect_failed: '加入失败，将重试', left: '已退出', room_closed: '房间已解散' };
const help: Record<string, string> = { assigned: '加入请求已保存，设备正在处理。', waiting_device: '请开机联网，联网后自动加入。', suspended: '设备正在通话，结束后会自动回到房间。', connecting: '设备正在连接房间，请稍候。', joined: '设备已进入房间，可以收听并按住说话键发言。', connect_failed: '设备暂未连接成功，正在自动重试，请检查设备网络。' };
export default function Room({ deviceId, onClose, onUpdate }: { deviceId?: string; onClose?: () => void; onUpdate?: (assignment: Assignment) => void } = {}) {
  const { t } = useLanguage();
  const device = deviceId ?? new URLSearchParams(location.search).get('device_id') ?? '';
  const [mode, setMode] = useState<'create' | 'join'>('create');
  const [snapshot, setSnapshot] = useState<Snapshot>(), [loading, setLoading] = useState(true), [busy, setBusy] = useState(false);
  const [message, setMessage] = useState(''), [notice, setNotice] = useState('');
  const [code, setCode] = useState(''), [newPassword, setNewPassword] = useState(''), [password, setPassword] = useState(''), [usePassword, setUsePassword] = useState(false);
  const [leave, setLeave] = useState<Assignment>();
  const pending = useRef(false), sequence = useRef(0), cache = useRef('');
  const url = '/v1/call/group/web/device/' + encodeURIComponent(device);
  function store(value: Snapshot) { setSnapshot(value); onUpdate?.(value.assignment); if (cache.current) saveSnapshot(cache.current, value); }
  async function refresh() {
    if (pending.current) return;
    const current = ++sequence.current; setLoading(true); setMessage(''); setNotice('');
    try {
      const list = await request<{ device_id: string; device_name: string }[]>('/v1/user/device/list');
      const owned = list.find(item => item.device_id === device);
      if (!owned) { if (current === sequence.current) setMessage('设备不属于当前账号，请返回设备列表'); return; }
      const assignment = await request<Assignment>(url);
      if (current === sequence.current) store({ name: owned.device_name, assignment, verified: true });
    } catch (error) { if (current === sequence.current) setMessage(errorText(error)); }
    finally { if (current === sequence.current) setLoading(false); }
  }
  useEffect(() => {
    let alive = true;
    const away = () => { if (cache.current) markAway(cache.current); };
    window.addEventListener('pagehide', away);
    void (async () => {
      if (!device) { setMessage('请返回设备列表，选择一台设备后打开多人对讲'); setLoading(false); return; }
      try {
        cache.current = await snapshotKey('room:' + device);
        const saved = restoreSnapshot<Snapshot>(cache.current);
        if (!alive) return;
        if (saved?.assignment && typeof saved.name === 'string') { setSnapshot(saved); setLoading(false); setNotice('请点击刷新获取状态'); return; }
      } catch { /* Cache is optional. */ }
      if (alive) await refresh();
    })();
    return () => { alive = false; sequence.current++; window.removeEventListener('pagehide', away); };
  }, [device]);
  async function change(kind: 'create' | 'join' | 'leave', body: object) {
    if (pending.current || !snapshot) return;
    pending.current = true; const current = ++sequence.current;
    setBusy(true); setMessage(''); setNotice(''); setLeave(undefined);
    try {
      const assignment = await request<Assignment>(url + '/' + kind, { method: 'POST', body });
      if (current !== sequence.current) return;
      store({ ...snapshot, assignment, verified: false });
      setNewPassword(''); setPassword('');
      try {
        const live = await request<Assignment>(url);
        if (current === sequence.current) { store({ ...snapshot, assignment: live, verified: true }); setNotice(kind === 'leave' ? '已退出房间，可重新创建或加入其他房间。' : '加入请求已保存，设备加入进度见下方。'); }
      } catch { if (current === sequence.current) setNotice('操作已成功，状态更新失败；请点击刷新获取最新状态。'); }
    } catch (error) { if (current === sequence.current) setMessage(errorText(error)); }
    finally { pending.current = false; if (current === sequence.current) { setBusy(false); setLoading(false); } }
  }
  const a = snapshot?.assignment;
  const submit = (kind: 'create' | 'join', body: object) => (event: FormEvent) => { event.preventDefault(); void change(kind, body); };
  const content = <main className={onClose ? "room-workspace" : "narrow"}>
    <div className="row">{onClose ? <div><strong>{snapshot?.name || t('未命名设备')}</strong>{snapshot && <span className="room-online muted">{t(snapshot.verified ? a?.online ? '● 在线' : '○ 离线' : '状态待更新')}</span>}<p className="muted room-device-id">{device}</p></div> : <h1>{t('多人对讲')}</h1>}<button type="button" disabled={loading || busy || !device} onClick={() => void refresh()}>{t('刷新')}</button></div>
    {!onClose && <p className="muted">{snapshot?.name || t('未命名设备')} · {device}</p>}
    {loading && <p role="status">{t('正在加载房间信息…')}</p>}{message && <p role="alert" className="error">{t(message)}</p>}{notice && <p role="status" className="muted">{t(notice)}</p>}
    {snapshot && <>{!onClose && <p className="muted">{t(snapshot.verified ? a?.online ? '● 在线' : '○ 离线' : '状态待更新')}</p>}{a?.desired_state === 'joined' ? <section className="card"><div className="row"><h2>{t('当前房间')}</h2><span className="muted">{t(a.password_set ? '已设置房间密码' : '无密码房间')}</span></div>
      <div className="room-code"><small>{t('房间号')}</small><strong>{a.room_code}</strong><p>{t('其他设备可通过此房间号加入')}</p></div>
      <div className="row"><strong className="success">{t(states[a.state] || '正在同步')}</strong><span className="muted">{t('在线设备：')}{snapshot.verified ? a.online_count ?? 0 : t('待更新')}</span></div><p className="muted">{t(help[a.state] || '正在同步设备状态，请稍候。')}</p>
      <button className="danger wide" disabled={busy} onClick={() => setLeave(a)}>{t('退出房间')}</button>
    </section> : <fieldset disabled={busy || loading}>
      {a?.state === 'room_closed' && <p className="muted">{t('房间已解散，可新建或加入其他房间')}</p>}
      {onClose && <nav className="filter-tabs" aria-label={t('房间操作')}><button type="button" aria-pressed={mode === 'create'} onClick={() => setMode('create')}>{t('创建房间')}</button><button type="button" aria-pressed={mode === 'join'} onClick={() => setMode('join')}>{t('加入房间')}</button></nav>}
      <form hidden={!!onClose && mode !== 'create'} className="card" onSubmit={submit('create', { password: usePassword ? newPassword : '' })}>{!onClose && <h2>{t('创建房间')}</h2>}{!onClose && <p className="muted">{t('创建后，此设备自动加入房间。')}</p>}<div className="radio-group"><label className="radio"><input name="password-mode" type="radio" checked={!usePassword} onChange={() => { setUsePassword(false); setNewPassword(''); }}/>{t('无密码')}</label><label className="radio"><input name="password-mode" type="radio" checked={usePassword} onChange={() => setUsePassword(true)}/>{t('设置密码')}</label></div>
      {usePassword && <label>{t('4 位数字密码')}<input type="password" inputMode="numeric" autoComplete="new-password" required pattern="[0-9]{4}" value={newPassword} onChange={event => setNewPassword(event.target.value.replace(/\D/g, '').slice(0, 4))}/></label>}
      <button className="primary wide">{t('创建房间')}</button></form>
      <form hidden={!!onClose && mode !== 'join'} className={onClose ? 'card room-join-form' : 'card'} onSubmit={submit('join', { room_code: code, password })}>{!onClose && <h2>{t('加入房间')}</h2>}{!onClose && <p className="muted">{t('输入已有房间的房间号，与其他设备对讲。')}</p>}<label>{t('6 位房间号')}<input name="group-room-code" inputMode="numeric" autoComplete="off" pattern="[0-9]{6}" required value={code} onChange={event => setCode(event.target.value.replace(/\D/g, '').slice(0, 6))}/></label><label>{t('房间密码')}{!onClose && t('（无密码房间留空）')}<input placeholder={onClose ? t('无密码则留空') : undefined} name="group-room-password" type="password" inputMode="numeric" autoComplete="new-password" pattern="[0-9]{4}" value={password} onChange={event => setPassword(event.target.value.replace(/\D/g, '').slice(0, 4))}/></label><button className="wide">{t('加入房间')}</button></form>
    </fieldset>}</>}
    <p className="muted">{t('加入后默认收听，按住设备上的说话键发言。')}<br/>{t('离线设备上线后自动加入；房间连续无人在线 24 小时后自动解散。')}</p>
    {leave && <Dialog title="退出房间？" onClose={() => setLeave(undefined)}><p>{snapshot?.name || t('未命名设备')} · {leave.room_code}</p><p className="muted">{t('退出后可再次通过房间号加入。')}</p><div className="row"><button onClick={() => setLeave(undefined)}>{t('取消')}</button><button className="danger" onClick={() => void change('leave', { room_id: leave.room_id, assignment_version: leave.assignment_version })}>{t('确认退出')}</button></div></Dialog>}
  </main>;
  return onClose ? <Dialog title="多人对讲" size="compact" onClose={onClose} closeDisabled={busy}>{content}</Dialog> : <Shell title="多人对讲">{content}</Shell>;
}
