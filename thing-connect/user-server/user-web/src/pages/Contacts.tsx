import { Input, Segmented, List, Empty, Collapse, Button as ActionButton } from 'antd';
import { Button } from '../components/Button';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Dialog } from '../components/Dialog';
import { Shell } from '../components/Shell';
import { errorText, request } from '../lib/api';
import { t, useLanguage } from '../lib/i18n';

interface Device { device_id: string; device_name: string; status: number }
interface Contact { id: number; type: string; source: string; device_id: string; device_name?: string; remark?: string; online?: boolean; wx_app_id?: string }
interface Pending { id: number; initiator_device: string; target_device: string }
const name = (c: Contact) => c.remark?.trim() || fallback(c);
const fallback = (c: Contact) => c.type === 'voip' ? t('微信用户') : c.device_name?.trim() || c.device_id;
const category = (c: Contact) => c.type === 'voip' ? 'voip' : c.source === 'auto' ? 'mine' : 'manual';
const key = (c: Contact) => JSON.stringify([c.type, c.id, c.device_id, c.wx_app_id]);
export default function Contacts({ deviceId, onClose }: { deviceId?: string; onClose?: () => void } = {}) {
  const { t } = useLanguage();
  const [devices, setDevices] = useState<Device[]>(), [device, setDevice] = useState(deviceId ?? new URLSearchParams(location.search).get('device_id') ?? '');
  const [error, setError] = useState('');
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setError('');
    void request<Device[]>('/v1/user/device/list', { signal: controller.signal }).then(data => {
      const bound = data.filter(d => d.status === 1); setDevices(bound);
      if (onClose && !bound.some(d => d.device_id === deviceId)) { setDevice(''); setError('设备不属于当前账号，请返回设备列表'); }
      else setDevice(id => bound.some(d => d.device_id === id) ? id : bound[0]?.device_id ?? '');
    }).catch(e => { if (!controller.signal.aborted) setError(errorText(e)); });
    return () => controller.abort();
  }, [refresh]);
  const content = <main className={onClose ? "contacts-workspace" : "content"}>
    {onClose ? <div className="contact-device-context"><strong>{devices?.find(d => d.device_id === device)?.device_name || t(devices ? '未命名设备' : '正在加载设备…')}</strong><span className="muted">{device}</span>{error && <p role="alert" className="error">{t(error)} <Button onClick={() => setRefresh(value => value + 1)}>{t('重试')}</Button></p>}</div> : <section className="card device-selector"><label>{t('当前管理设备')}<select disabled={!devices?.length} value={device} onChange={event => { setDevice(event.target.value); if (onClose) return; const url = new URL(location.href); url.searchParams.set('device_id', event.target.value); history.replaceState(null, '', url); }}>
      {!devices?.length && <option>{t(devices ? '暂无已绑定设备' : '正在加载设备…')}</option>}{devices?.map(d => <option value={d.device_id} key={d.device_id}>{d.device_name || t('未命名设备')} · {d.device_id}</option>)}
    </select></label>{error && <p role="alert" className="error">{error} <Button onClick={() => setRefresh(value => value + 1)}>{t('重试')}</Button></p>}</section>}
    {device && devices && <ContactList key={device} device={device} devices={devices}/>}
  </main>;
  return onClose ? <Dialog title="联系人" size="workspace" onClose={onClose}>{content}</Dialog> : <Shell title="联系人管理">{content}</Shell>;
}
function ContactList({ device, devices }: { device: string; devices: Device[] }) {
  const { t } = useLanguage();
  const [rows, setRows] = useState<Contact[]>([]), [pending, setPending] = useState<Pending[]>([]);
  const [loading, setLoading] = useState(true), [error, setError] = useState(''), [pendingError, setPendingError] = useState('');
  const [search, setSearch] = useState(''), [filter, setFilter] = useState('all'), [editing, setEditing] = useState<string>();
  const [add, setAdd] = useState(false), [remove, setRemove] = useState<Contact>();
  const [handling, setHandling] = useState<number[]>([]);
  const listRevision = useRef(0), pendingRevision = useRef(0), responding = useRef(new Set<number>()), alive = useRef(true);
  async function load() {
    const rev = ++listRevision.current; setLoading(true); setError('');
    try { const data = await request<{ contacts: Contact[] }>('/v1/call/user/contacts?device_id=' + encodeURIComponent(device)); if (alive.current && rev === listRevision.current) setRows(data.contacts || []); }
    catch (e) { if (alive.current && rev === listRevision.current) setError(errorText(e)); }
    finally { if (alive.current && rev === listRevision.current) setLoading(false); }
  }
  async function loadPending() {
    const rev = ++pendingRevision.current; setPendingError('');
    try { const data = await request<{ pending: Pending[] }>('/v1/call/user/contacts/pending'); if (alive.current && rev === pendingRevision.current) setPending(data.pending || []); }
    catch (e) { if (alive.current && rev === pendingRevision.current) setPendingError(errorText(e)); }
  }
  useEffect(() => { alive.current = true; void load(); void loadPending(); return () => { alive.current = false; listRevision.current++; pendingRevision.current++; }; }, [device]);
  function updated(update: (rows: Contact[]) => Contact[]) { listRevision.current++; setLoading(false); setRows(update); setError(''); }
  async function respond(item: Pending, action: string) {
    if (responding.current.has(item.id)) return;
    responding.current.add(item.id); setHandling([...responding.current]);
    try {
      await request('/v1/call/user/contacts/respond', { method: 'POST', body: { id: item.id, action } });
      if (!alive.current) return;
      pendingRevision.current++; setPending(items => items.filter(p => p.id !== item.id)); setPendingError('');
      if (action === 'accept' && item.target_device === device) void load();
    } catch (e) { if (alive.current) setPendingError(errorText(e)); }
    finally { responding.current.delete(item.id); if (alive.current) setHandling([...responding.current]); }
  }
  const filtered = rows.filter(c => (!search.trim() || [c.remark, c.device_name, c.device_id].some(v => v?.toLowerCase().includes(search.trim().toLowerCase()))) && (filter === 'all' || (filter === 'voip' ? c.type === 'voip' : c.type !== 'voip' && c.source === (filter === 'mine' ? 'auto' : 'manual'))));
  const categories = new Set<string>(rows.map(category));
  const filterOptions = Object.entries({ all: '全部', mine: '我的设备', manual: '手动添加', voip: '微信授权' }).filter(([value]) => value === 'all' || categories.has(value) || value === filter).map(([value,label])=>({value,label:t(label),title:''}));
  const showFilters = categories.size > 1 || filter !== 'all';
  const showSearch = rows.length > 5 || showFilters || !!search;
  return <div className="contact-list">
    <div className="contact-toolbar"><span className="muted">{t('联系人共 {count} 位', { count: rows.length })}</span><div className="contact-actions"><Button disabled={loading} onClick={() => { void load(); void loadPending(); }}>{t('刷新')}</Button><Button className="primary" onClick={() => setAdd(true)}>{t('添加联系人')}</Button></div></div>
    {showSearch && <Input type="search" aria-label={t('搜索名称或设备 ID')} placeholder={t('搜索名称或设备 ID')} value={search} onChange={e => setSearch(e.target.value)}/>}
    {showFilters && <nav className="contact-filter" aria-label={t('联系人类型')}><Segmented block value={filter} onChange={setFilter} options={filterOptions}/></nav>}
    <div className="contact-scroll">{loading && <p role="status">{t('联系人加载中…')}</p>}{error && <p role="alert" className="error">{error}</p>}
      {(pending.length > 0 || !!pendingError) && <Collapse className="contact-requests" defaultActiveKey={['pending']} items={[{key:'pending',label:t('待审批') + ' (' + pending.length + ')',children:<>
        {pendingError && <p className="error" role="alert">{pendingError} <Button onClick={() => void loadPending()}>{t('重试')}</Button></p>}
        {pending.map(item => <div className="pending-row" key={item.id}><p>{item.initiator_device} → {devices.find(d => d.device_id === item.target_device)?.device_name || t('未命名设备')} · {item.target_device}</p><div className="row"><Button disabled={handling.includes(item.id)} onClick={() => void respond(item, 'reject')}>{t('拒绝')}</Button><Button className="primary" disabled={handling.includes(item.id)} onClick={() => void respond(item, 'accept')}>{t('同意')}</Button></div></div>)}
      </>}]}/>}
      {!loading && !filtered.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t(rows.length ? '没有匹配的联系人' : '暂无联系人')}>{rows.length > 0 && <Button onClick={() => { setSearch(''); setFilter('all'); }}>{t('清除搜索和筛选')}</Button>}</Empty>}
      <List className="contact-entries" dataSource={filtered} rowKey={key} locale={{ emptyText: null }} renderItem={c=><List.Item className="contact-entry">
        <div className="contact-entry-main"><div className="contact-identity"><strong>{name(c)}</strong>{categories.size > 1 && <span className="badge">{t(c.type === 'voip' ? '微信授权' : c.source === 'auto' ? '我的设备' : '手动添加')}</span>}{c.type !== 'voip' && <span className={c.online ? 'success' : 'muted'}>{t(c.online ? '在线' : '离线')}</span>}</div>{c.type !== 'voip' && <p className="muted contact-peer-id">{c.device_id}</p>}</div>
        <div className="contact-entry-actions"><ActionButton type="text" onClick={() => setEditing(key(c))}>{t('改备注')}</ActionButton>{c.source === 'manual' && c.type !== 'voip' && <ActionButton type="text" danger onClick={() => setRemove(c)}>{t('删除')}</ActionButton>}</div>
        {editing === key(c) && <div className="contact-remark"><Remark key={key(c)} contact={c} device={device} close={() => setEditing(undefined)} saved={remark => { updated(items => items.map(item => key(item) === key(c) ? { ...item, remark } : item)); setEditing(undefined); }}/></div>}
      </List.Item>}/>

    </div><p className="muted contact-help">{t('我的设备自动关联；手动添加需对方同意；微信联系人来自小程序授权。')}</p>{add && <AddContact device={device} devices={devices} contacts={rows} close={() => setAdd(false)} accepted={() => { if (alive.current) void load(); }}/>}
    {remove && <RemoveContact contact={remove} close={() => setRemove(undefined)} removed={() => { if (alive.current) { updated(items => items.filter(item => key(item) !== key(remove))); setRemove(current => current === remove ? undefined : current); } }}/>}
  </div>;
}
function Remark({ contact, device, close, saved }: { contact: Contact; device: string; close(): void; saved(value: string): void }) {
  const { t } = useLanguage();
  const [value, setValue] = useState(contact.remark || ''), [error, setError] = useState(''), [busy, setBusy] = useState(false);
  const alive = useRef(true), pending = useRef(false), trigger = useRef(document.activeElement);
  useEffect(() => () => { alive.current = false; if (trigger.current instanceof HTMLElement && trigger.current.isConnected) trigger.current.focus(); }, []);
  async function submit(event: FormEvent) {
    event.preventDefault(); if (pending.current) return;
    const remark = value.trim(); if (Array.from(remark).length > 64) { setError('备注不能超过 64 个字符'); return; }
    pending.current = true; setBusy(true); setError('');
    try { await request('/v1/call/user/contacts/remark', { method: 'PUT', body: { device_id: device, peer_id: contact.device_id, remark } }); if (alive.current) saved(remark); }
    catch (e) { if (alive.current) setError(errorText(e)); }
    finally { pending.current = false; if (alive.current) setBusy(false); }
  }
  return <form onSubmit={event => void submit(event)}><fieldset disabled={busy}><label>{t('联系人备注')}<Input autoFocus value={value} onChange={e => setValue(e.target.value)} onKeyDown={e => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); } }}/></label><p className="muted">{t('清空后显示“{name}”；仅对当前设备生效。最多 64 个字符。', { name: fallback(contact) })}</p>{error && <p className="error" role="alert">{t(error)}</p>}<div className="row"><Button className="primary" type="submit">{t(busy ? '保存中…' : '保存')}</Button><Button type="button" onClick={close}>{t('取消')}</Button></div></fieldset></form>;
}
function AddContact({ device, devices, contacts, close, accepted }: { device: string; devices: Device[]; contacts: Contact[]; close(): void; accepted(): void }) {
  const { t } = useLanguage();
  const [target, setTarget] = useState(''), [error, setError] = useState(''), [busy, setBusy] = useState(false), [sent, setSent] = useState('');
  const alive = useRef(true), pending = useRef(false);
  useEffect(() => () => { alive.current = false; }, []);
  async function submit(event: FormEvent) {
    event.preventDefault(); if (pending.current || sent) return;
    const id = target.trim();
    const invalid = !id ? '请输入对方设备 ID' : id === device ? '不能添加自己为联系人' : contacts.some(c => c.type !== 'voip' && c.device_id === id) ? '该设备已在联系人列表中' : devices.some(d => d.device_id === id) ? '我的设备会自动关联，无需申请' : '';
    if (invalid) { setError(invalid); return; }
    pending.current = true; setBusy(true); setError('');
    try { const data = await request<{ status: string }>('/v1/call/user/contacts/request', { method: 'POST', body: { device_id: device, target_device_id: id } }); if (alive.current) { setSent(data.status === 'accepted' ? 'accepted' : 'pending'); if (data.status === 'accepted') accepted(); } }
    catch (e) { if (alive.current) setError(errorText(e)); }
    finally { pending.current = false; if (alive.current) setBusy(false); }
  }
  return <Dialog title="添加联系人" onClose={close}><p className="muted">{devices.find(d => d.device_id === device)?.device_name || t('未命名设备')} · {device}</p>{sent ? <p className="success" role="status">{t(sent === 'accepted' ? '已成为联系人，可以开始联系' : '申请已发送，等待对方同意')}</p> : <form onSubmit={e => void submit(e)}><label>{t('对方设备 ID')}<Input autoFocus required disabled={busy} value={target} onChange={e => setTarget(e.target.value)}/></label><p className="muted">{t('同账号下的设备会自动互相关联，不需要手动申请。')}</p>{error && <p role="alert" className="error">{t(error)}</p>}<Button className="primary wide" disabled={busy}>{t('发起申请')}</Button></form>}</Dialog>;
}
function RemoveContact({ contact, close, removed }: { contact: Contact; close(): void; removed(): void }) {
  const { t } = useLanguage();
  const [busy, setBusy] = useState(false), [error, setError] = useState('');
  const alive = useRef(true), pending = useRef(false);
  useEffect(() => () => { alive.current = false; }, []);
  async function remove() {
    if (pending.current) return; pending.current = true; setBusy(true);
    try { await request('/v1/call/user/contacts/' + encodeURIComponent(contact.id), { method: 'DELETE' }); removed(); }
    catch (e) { if (alive.current) setError(errorText(e)); }
    finally { pending.current = false; if (alive.current) setBusy(false); }
  }
  return <Dialog title="删除联系人？" onClose={close}><p>{name(contact)} · {contact.device_id}</p><p className="muted">{t('删除后双方都不再是好友，需要重新申请并同意后才能恢复联系。')}</p>{error && <p className="error" role="alert">{error}</p>}<div className="row"><Button onClick={close}>{t('取消')}</Button><Button className="danger" disabled={busy} onClick={() => void remove()}>{t('删除')}</Button></div></Dialog>;
}
