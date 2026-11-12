import { RoleEditor } from './editor';
import Taro from '@tarojs/taro';
import { Text, View } from '@tarojs/components';
import { useEffect, useState } from 'react';
import { Action, SearchField, Notice } from '../../components/ui';
import { useDeviceWorkspace } from '../../hooks/device-workspace';
import { request } from '../../services/api';
import { devicesChanged } from '../../services/device-events';
import { draftOf, rolePayload, roleRegionLabel, type Role, type RoleDraft, type Voice } from '../../domain/roles';
interface Entry extends Role { id: string; preset?: boolean; unavailable?: boolean }
interface Binding { role_id: string; default_role_id: string }
interface Workspace { roles: Entry[]; binding: Binding; voices: Voice[] }
export default function Roles() {
  const [selected, setSelected] = useState<Entry>(), [draft, setDraft] = useState<RoleDraft>(), [search, setSearch] = useState(''), [notice, setNotice] = useState('');
  const w = useDeviceWorkspace<Workspace>(async id => {
    const [owned, binding, voices] = await Promise.all([request<{ items: Entry[] }>('/v1/ai/roles'), request<Binding>(url(id)), request<{ items: Voice[] }>('/v1/ai/voices')]);
    let roles = owned.items || [];
    if (binding.default_role_id) {
      let preset: Entry = { id: binding.default_role_id, name: '默认角色', preset: true, unavailable: true };
      try { preset = { ...await request<Role>('/v1/ai/roles/default'), id: binding.default_role_id, preset: true }; } catch { /* Other owned roles remain available. */ }
      roles = [preset, ...roles.filter(r => r.id !== preset.id)];
    }
    const active = binding.role_id || binding.default_role_id;
    if (active && !roles.some(r => r.id === active)) {
      try { roles.push({ ...await request<Role>('/v1/ai/roles/' + encodeURIComponent(active)), id: active }); }
      catch { roles.push({ id: active, name: '当前角色暂不可用', unavailable: true }); }
    }
    return { roles, binding, voices: voices.items || [] };
  });
  function url(id: string) { return '/v1/ai/device/' + encodeURIComponent(id) + '/role'; }
  const filteredRoles = w.value?.roles.filter(r => (r.name || '').toLowerCase().includes(search.trim().toLowerCase())) || [];
  const active = w.value?.binding.role_id || w.value?.binding.default_role_id;
  async function edit(role?: Entry) {
    await w.run(async () => {
      const full: Entry = role ? role.preset ? role : { ...role, ...await request<Role>('/v1/ai/roles/' + encodeURIComponent(role.id)) } : { id: '', name: '' };
      if (w.alive.current) { setSelected(full); setDraft(draftOf(full)); setNotice(''); void Taro.pageScrollTo({ scrollTop: 0, duration: 0 }); }
    });
  }
  async function closeEditor() {
    if (w.busy) return;
    if (selected && draft && !selected.preset && JSON.stringify(draft) !== JSON.stringify(draftOf(selected))) {
      const answer = await Taro.showModal({ title: '修改尚未保存', content: '放弃当前修改并返回角色列表？', confirmText: '放弃修改' });
      if (!answer.confirm || !w.alive.current) return;
    }
    setSelected(undefined); setDraft(undefined); w.setError('');
  }
  async function save() {
    if (!selected || !draft || !w.value || selected.preset) return;
    let payload: Role;
    try { payload = rolePayload(selected, draft, w.value.voices); }
    catch (e) { w.setError(e instanceof Error ? e.message : '请检查角色设置'); return; }
    await w.run(async () => {
      const result = await request<Role>('/v1/ai/roles' + (selected.id ? '/' + encodeURIComponent(selected.id) : ''), selected.id ? 'PUT' : 'POST', payload);
      const id = result.id || selected.id;
      if (!id) throw new Error('保存结果暂不可用，请重新加载角色列表');
      const saved: Entry = { ...selected, ...payload, ...result, id };
      w.commit({ ...w.value!, roles: [...w.value!.roles.filter(r => r.id !== id), saved] }); devicesChanged();
      if (w.alive.current) { setSelected(saved); setDraft(draftOf(saved)); setNotice('角色已保存'); }
    });
  }
  async function useRole(role: Entry) {
    if (!w.device || !w.value || role.unavailable) return;
    await w.run(async () => {
      const answer = await Taro.showModal({ title: '切换 AI 角色', content: `为此设备使用“${role.name || '未命名角色'}”？`, confirmText: '使用角色' });
      if (!answer.confirm || !w.alive.current) return;
      await request(url(w.device!.device_id), role.preset ? 'DELETE' : 'PUT', role.preset ? undefined : { role_id: role.id });
      w.commit({ ...w.value!, binding: { ...w.value!.binding, role_id: role.preset ? '' : role.id } }); devicesChanged();
      if (w.alive.current) setNotice('设备角色已切换');
    });
  }
  async function remove(role: Entry) {
    if (!w.value || role.preset || role.id === active) return;
    await w.run(async () => {
      const answer = await Taro.showModal({ title: '删除角色', content: `确定删除“${role.name || '未命名角色'}”？`, confirmText: '删除', confirmColor: '#fa5151' });
      if (!answer.confirm || !w.alive.current) return;
      await request('/v1/ai/roles/' + encodeURIComponent(role.id), 'DELETE');
      w.commit({ ...w.value!, roles: w.value!.roles.filter(r => r.id !== role.id) }); devicesChanged();
      if (w.alive.current) setNotice('角色已删除');
    });
  }
  const dirty = !!selected && !!draft && !selected.preset && JSON.stringify(draft) !== JSON.stringify(draftOf(selected));
  useEffect(() => {
    if (process.env.TARO_ENV !== 'weapp') return;
    if (dirty) void Taro.enableAlertBeforeUnload({ message: '角色修改尚未保存，确定离开？' });
    else void Taro.disableAlertBeforeUnload();
    return () => { void Taro.disableAlertBeforeUnload(); };
  }, [dirty]);
  if (selected && draft) return <View className={'page content-page role-editor-page' + (selected.preset ? ' role-readonly' : '')}>
    <View className='row between'><Action disabled={w.busy} onClick={() => void closeEditor()}>‹ 返回角色列表</Action><Text className='title'>{selected.preset ? '默认角色' : selected.id ? '编辑角色' : '创建角色'}</Text></View>
    <View className='role-device-context'><Text className='device-context-label'>当前设备</Text><Text className='device-context-name'>{w.device?.device_name || '设备信息待加载'}</Text></View>
    <Notice error>{w.error}</Notice><Notice>{notice}</Notice>
    <RoleEditor key={selected.id || 'new'} role={selected} draft={draft} voices={w.value?.voices || []} busy={w.busy} patch={value => { if (!w.busy && !selected.preset) { setDraft(current => current ? { ...current, ...value } : current); setNotice(''); } }} />
    {!selected.preset && <View className='role-editor-actions'><Text className='muted'>保存影响共用设备；切换角色请返回列表。</Text><View className='row'><Action disabled={w.busy || !dirty} onClick={async () => { const answer = await Taro.showModal({ title: '放弃修改', content: '恢复到上次保存的角色设置？', confirmText: '放弃修改' }); if (answer.confirm && w.alive.current) setDraft(draftOf(selected)); }}>放弃修改</Action><Action primary disabled={w.busy || !dirty} onClick={() => void save()}>{w.busy ? '保存中…' : '保存角色'}</Action></View></View>}
  </View>;
  return <View className='page content-page'><View className='row between'><Text className='title device-page-name'>{w.device?.device_name || '当前设备'}</Text><Action disabled={w.busy} onClick={() => void w.refresh()}>刷新</Action></View><Text className='device-page-description'>为这台设备选择 AI 的性格、声音和回复方式。点击“使用角色”后，当前设备才会切换角色。</Text><Notice error>{w.error}</Notice><Notice>{w.busy ? '正在处理…' : notice}</Notice>
    <View className='role-list-tools'><View className='row between'><Text className='role-list-label'>我的角色</Text><Action primary disabled={w.busy || !w.value} onClick={() => void edit()}>＋ 创建角色</Action></View><SearchField value={search} onChange={setSearch} placeholder='搜索角色名称' />{w.value && <Text className='muted'>{search.trim() ? '找到 ' : '共 '}{filteredRoles.length} 个角色</Text>}</View>
    {w.value && !filteredRoles.length && <View className='empty'>{search.trim() ? '没有匹配的角色，换个关键词试试' : '暂无角色，点击上方创建角色'}</View>}
    {filteredRoles.map(role => <View className='card' key={role.id}><View className='row between'><Text className='label'>{role.name || '未命名角色'}</Text>{role.id === active && <Text className='badge'>当前使用</Text>}</View><Text className='muted'>{role.preset ? '默认角色 · ' : ''}{roleRegionLabel(role.region)}{role.unavailable ? ' · 暂不可用，请刷新重试' : ''}</Text><View className='row'><Action disabled={w.busy || role.unavailable} onClick={() => void edit(role)}>{role.preset ? '查看' : '编辑'}</Action><Action primary disabled={w.busy || role.unavailable || role.id === active} onClick={() => void useRole(role)}>使用角色</Action>{!role.preset && <Action danger disabled={w.busy || role.id === active} onClick={() => void remove(role)}>删除</Action>}</View></View>)}

  </View>;
}
