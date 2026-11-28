import Taro, { useLoad } from '@tarojs/taro';
import { Button, Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Action, Field, Notice, SearchField, Sheet } from '../../components/ui';
import { ApiError, errorText, request } from '../../services/api';
import { readDevices } from '../../services/device-directory';
import type { Device } from '../../domain/devices';
import { canRemove, contactKey, contactName, filterContacts, validateContactTarget, type Contact, type PendingContact } from '../../domain/contacts';

export default function Contacts() {
  const [deviceId, setDeviceId] = useState(''), [devices, setDevices] = useState<Device[]>([]);
  const [rows, setRows] = useState<Contact[]>([]), [pending, setPending] = useState<PendingContact[]>([]);
  const [loading, setLoading] = useState(true), [error, setError] = useState(''), [pendingError, setPendingError] = useState('');
  const [search, setSearch] = useState(''), [filter, setFilter] = useState('all');
  const [editor, setEditor] = useState<'add' | Contact>(), [value, setValue] = useState(''), [editorError, setEditorError] = useState(''), [sent, setSent] = useState('');
  const [busy, setBusy] = useState(false);
  const alive = useRef(true), revision = useRef(0), writing = useRef(false), currentId = useRef('');
  useEffect(() => () => { alive.current = false; revision.current++; }, []);
  useLoad(options => { currentId.current = options.device_id || ''; setDeviceId(currentId.current); void load(false); });
  async function load(fresh = true) {
    const rev = ++revision.current, id = currentId.current;
    const current = () => alive.current && rev === revision.current;
    setLoading(true); setError(''); setPendingError('');
    try {
      const list = await readDevices(fresh);
      if (!current()) return;
      if (!id || !list.some(d => d.device_id === id)) throw new ApiError(40300, '设备不属于当前账号，请返回设备列表');
      setDevices(list);
      await Promise.all([
        request<{ contacts: Contact[] }>('/v1/call/user/contacts?device_id=' + encodeURIComponent(id))
          .then(result => { if (current()) setRows(result.contacts || []); })
          .catch(e => { if (current()) setError(errorText(e)); }),
        request<{ pending: PendingContact[] }>('/v1/call/user/contacts/pending')
          .then(result => { if (current()) setPending(result.pending || []); })
          .catch(e => { if (current()) setPendingError(errorText(e)); }),
      ]);
    } catch (e) { if (current()) { setDevices([]); setRows([]); setPending([]); setError(errorText(e)); } }
    finally { if (current()) setLoading(false); }
  }
  async function mutate(operation: () => Promise<void>, inEditor = false) {
    if (writing.current) return;
    writing.current = true; setBusy(true); if (inEditor) setEditorError(''); else setError('');
    // A response started before a mutation cannot overwrite its successful result.
    revision.current++; setLoading(false);
    try { await operation(); }
    catch (e) { if (alive.current) (inEditor ? setEditorError : setError)(errorText(e)); }
    finally { writing.current = false; if (alive.current) setBusy(false); }
  }
  function openEditor(target: 'add' | Contact) { setEditor(target); setValue(target === 'add' ? '' : target.remark || ''); setEditorError(''); setSent(''); }
  async function save() {
    if (!editor || sent) return;
    const text = value.trim();
    const validation = editor === 'add' ? validateContactTarget(text, deviceId, devices.map(d => d.device_id), rows) : Array.from(text).length > 64 ? '备注不能超过 64 个字符' : '';
    if (validation) { setEditorError(validation); return; }
    const target = editor;
    await mutate(async () => {
      if (target === 'add') {
        const result = await request<{ status: string }>('/v1/call/user/contacts/request', 'POST', { device_id: deviceId, target_device_id: text });
        if (!alive.current) return;
        setSent(result.status === 'accepted' ? '已成为联系人，可以开始联系' : '申请已发送，等待对方同意');
        if (result.status === 'accepted') await load();
      } else {
        await request('/v1/call/user/contacts/remark', 'PUT', { device_id: deviceId, peer_id: target.device_id, remark: text });
        if (alive.current) { setRows(items => items.map(item => contactKey(item) === contactKey(target) ? { ...item, remark: text } : item)); setEditor(undefined); }
      }
    }, true);
  }
  async function remove(contact: Contact) {
    if (busy || !canRemove(contact)) return;
    const { confirm } = await Taro.showModal({ title: '删除联系人？', content: '删除后双方都不再是好友，需要重新申请并同意后才能恢复联系。', confirmText: '删除', confirmColor: '#fa5151' });
    if (!confirm || !alive.current) return;
    await mutate(async () => { await request('/v1/call/user/contacts/' + contact.id, 'DELETE'); if (alive.current) { setRows(items => items.filter(c => contactKey(c) !== contactKey(contact))); setEditor(undefined); } }, true);
  }
  async function respond(item: PendingContact, action: 'accept' | 'reject') {
    await mutate(async () => {
      await request('/v1/call/user/contacts/respond', 'POST', { id: item.id, action });
      if (!alive.current) return;
      setPending(items => items.filter(p => p.id !== item.id));
      if (action === 'accept') await load();
    });
  }
  const device = devices.find(d => d.device_id === deviceId), filtered = filterContacts(rows, search, filter);
  return <View className='page content-page settings-page'>
    <View className='row between settings-header'><Text className='title device-page-name'>{device?.device_name || '当前设备'}</Text><Action disabled={busy || loading} onClick={() => void load()}>刷新</Action></View>
    <Notice error>{error}</Notice>
    <SearchField placeholder='搜索名称或设备 ID' value={search} onChange={setSearch} />
    <View className='tabs'>{Object.entries({ all: '全部', mine: '我的设备', manual: '手动添加', voip: '微信授权' }).map(([key, label]) => <Action key={key} primary={filter === key} onClick={() => setFilter(key)}>{label}</Action>)}</View>
    <View className='settings-section'>
      <View className='row between settings-section-title'><Text>联系人{!loading && !error ? ' · ' + filtered.length : ''}</Text><Action disabled={busy || !device} onClick={() => openEditor('add')}>＋ 添加联系人</Action></View>
      <View className='card settings-list'>
      {loading && <Notice>联系人加载中…</Notice>}
      {!loading && !filtered.length && <View className='empty'>{error ? '请刷新后重试' : rows.length ? '没有匹配的联系人' : '暂无联系人'}</View>}
      {filtered.map(contact => <Button className='settings-link settings-row' key={contactKey(contact)} disabled={busy} onClick={() => openEditor(contact)}>
        <View className='settings-copy'><Text>{contactName(contact)}</Text>{contact.type === 'device' && <Text className='settings-detail'>{contact.device_id} · {contact.online ? '在线' : '离线'}</Text>}</View>
        <Text className='settings-detail'>{contact.type === 'voip' ? '微信授权' : contact.source === 'auto' ? '我的设备' : '手动添加'}</Text><View className='settings-chevron' ariaHidden />
      </Button>)}
      </View>
      <Text className='settings-footnote'>同账号设备自动关联。添加其他设备需对方同意。</Text>
    </View>
    {(pending.length > 0 || pendingError) && <View className='card'><Text className='title'>待审批（{pending.length}）</Text><Notice error>{pendingError}</Notice>{pending.map(item => <View className='pending' key={item.id}><Text className='muted'>{item.initiator_device} → {devices.find(d => d.device_id === item.target_device)?.device_name || item.target_device}</Text><View className='row'><Action disabled={busy} onClick={() => void respond(item, 'reject')}>拒绝</Action><Action primary disabled={busy} onClick={() => void respond(item, 'accept')}>同意</Action></View></View>)}</View>}
    {editor && <Sheet title={editor === 'add' ? '添加联系人' : contactName(editor)} busy={busy} onClose={() => setEditor(undefined)}>{sent ? <Notice>{sent}</Notice> : <><Field label={editor === 'add' ? '对方设备 ID' : '联系人备注'} value={value} disabled={busy} onChange={setValue} /><Text className='muted'>{editor === 'add' ? '同账号设备自动关联，无需申请。' : '清空恢复原名，仅对当前设备生效，最多 64 个字符。'}</Text><Notice error>{editorError}</Notice><View className='wide'><Action primary disabled={busy} onClick={() => void save()}>{busy ? '提交中…' : editor === 'add' ? '发起申请' : '保存'}</Action></View>{editor !== 'add' && canRemove(editor) && <Action danger disabled={busy} onClick={() => void remove(editor)}>删除联系人</Action>}</>}</Sheet>}
  </View>;
}
