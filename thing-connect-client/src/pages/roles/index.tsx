import { tr, usePageLanguage } from '../../platform/translate';
import { RoleEditor } from './editor';
import Taro from '@tarojs/taro';
import { Button } from '../../platform/button';
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
  usePageLanguage();
  const [selected, setSelected] = useState<Entry>(), [draft, setDraft] = useState<RoleDraft>(), [search, setSearch] = useState('');
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
      if (role) {
        setSelected(role);
        setDraft(undefined);
      }
      const full: Entry = role ? role.preset ? role : { ...role, ...await request<Role>('/v1/ai/roles/' + encodeURIComponent(role.id)) } : { id: '', name: '' };
      if (w.alive.current) {
        setSelected(full);
        setDraft(draftOf(full));
        void Taro.pageScrollTo({ scrollTop: 0, duration: 0 });
      }
    });
  }
  async function closeEditor() {
    if (w.busy) return;
    if (selected && draft && !selected.preset && JSON.stringify(draft) !== JSON.stringify(draftOf(selected))) {
      const answer = await Taro.showModal({ title: tr("修改尚未保存"), content: tr("放弃当前修改并返回角色列表？"), confirmText: tr("放弃修改") });
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
      if (w.alive.current) { setSelected(saved); setDraft(draftOf(saved)); void Taro.showToast({ title: tr("角色已保存"), icon: 'success' }); }
    });
  }
  async function useRole(role: Entry) {
    if (!w.device || !w.value || role.unavailable) return;
    await w.run(async () => {
      const answer = await Taro.showModal({ title: tr("切换 AI 角色"), content: `为此设备使用“${role.name || '未命名角色'}”？`, confirmText: tr("使用角色") });
      if (!answer.confirm || !w.alive.current) return;
      await request(url(w.device!.device_id), role.preset ? 'DELETE' : 'PUT', role.preset ? undefined : { role_id: role.id });
      w.commit({ ...w.value!, binding: { ...w.value!.binding, role_id: role.preset ? '' : role.id } }); devicesChanged();
    });
  }
  async function remove(role: Entry) {
    if (!w.value || role.preset || role.id === active) return;
    await w.run(async () => {
      const answer = await Taro.showModal({ title: tr("删除角色"), content: `确定删除“${role.name || '未命名角色'}”？`, confirmText: tr("删除"), confirmColor: '#fa5151' });
      if (!answer.confirm || !w.alive.current) return;
      await request('/v1/ai/roles/' + encodeURIComponent(role.id), 'DELETE');
      w.commit({ ...w.value!, roles: w.value!.roles.filter(r => r.id !== role.id) }); devicesChanged();
      if (w.alive.current) { setSelected(undefined); setDraft(undefined); void Taro.showToast({ title: tr("角色已删除"), icon: 'success' }); }
    });
  }
  const dirty = !!selected && !!draft && !selected.preset && JSON.stringify(draft) !== JSON.stringify(draftOf(selected));
  useEffect(() => {
    if (process.env.TARO_ENV === 'h5') {
      if (!dirty) return;
      const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ''; };
      window.addEventListener('beforeunload', warn);
      return () => window.removeEventListener('beforeunload', warn);
    }
    if (dirty) void Taro.enableAlertBeforeUnload({ message: '角色修改尚未保存，确定离开？' });
    else void Taro.disableAlertBeforeUnload();
    return () => { void Taro.disableAlertBeforeUnload(); };
  }, [dirty]);
  if (selected && !draft) return <View className='page content-page settings-page'>
    <View className='row between settings-header'>
      <Text className='title device-page-name'>{selected.name || tr("未命名角色")}</Text>
      <Action disabled={w.busy} onClick={() => void closeEditor()}>{tr("返回角色列表")}</Action>
    </View>
    <Notice error>{w.error}</Notice>
    {w.busy && <Notice>{tr("正在处理…")}</Notice>}
    <Action disabled={w.busy} onClick={() => void edit(selected)}>{tr("重新加载角色详情")}</Action>
    {!selected.preset && selected.id !== active && <Action danger disabled={w.busy} onClick={() => void remove(selected)}>{tr("删除角色")}</Action>}
  </View>;
  if (selected && draft) return <View className={'page content-page settings-page role-editor-page' + (selected.preset ? ' role-readonly' : '')}>
    <View className='row between'><Action disabled={w.busy} onClick={() => void closeEditor()}>{tr("‹ 返回角色列表")}</Action><Text className='title'>{selected.preset ? tr("默认角色") : selected.id ? tr("编辑角色") : tr("创建角色")}</Text></View>
    <View className='role-device-context'><Text className='device-context-label'>{tr("当前设备")}</Text><Text className='device-context-name'>{w.device?.device_name || tr("设备信息待加载")}</Text></View>
    <Notice error>{w.error}</Notice>
    <RoleEditor key={selected.id || 'new'} role={selected} draft={draft} voices={w.value?.voices || []} busy={w.busy} patch={value => { if (!w.busy && !selected.preset) { setDraft(current => current ? { ...current, ...value } : current); } }} />
    {!selected.preset && <View className='role-editor-actions'><Text className='muted'>{tr("保存影响共用设备；切换角色请返回列表。")}</Text><View className='row'><Action disabled={w.busy || !dirty} onClick={async () => { const answer = await Taro.showModal({ title: tr("放弃修改"), content: tr("恢复到上次保存的角色设置？"), confirmText: tr("放弃修改") }); if (answer.confirm && w.alive.current) setDraft(draftOf(selected)); }}>{tr("放弃修改")}</Action><Action primary disabled={w.busy || !dirty} onClick={() => void save()}>{w.busy ? tr("保存中…") : tr("保存角色")}</Action></View></View>}
    {selected.id && !selected.preset && selected.id !== active && <Action danger disabled={w.busy} onClick={() => void remove(selected)}>{tr("删除角色")}</Action>}
  </View>;
  return <View className='page content-page settings-page'>
    <View className='row'><Text className='title device-page-name'>{w.device?.device_name || tr("当前设备")}</Text></View>
    <Notice error>{w.error}</Notice><Notice>{w.busy && !w.value ? tr("正在加载角色…") : ''}</Notice>
    {w.error && <Action disabled={w.busy} onClick={() => void w.refresh()}>{tr("重新加载角色列表")}</Action>}
    <SearchField value={search} onChange={setSearch} placeholder={tr("搜索角色名称")} />
    <View className='settings-section'>
      <View className='row between settings-section-title'><Text>{tr("我的角色")}{w.value ? ' · ' + filteredRoles.length : ''}</Text><Action disabled={w.busy || !w.value} onClick={() => void edit()}>{tr("＋ 创建角色")}</Action></View>
      <View className='card settings-list'>
        {w.value && !filteredRoles.length && <View className='empty'>{search.trim() ? tr("没有匹配的角色") : tr("暂无角色")}</View>}
        {filteredRoles.map(role => <View className='settings-row' key={role.id}>
          <Button className='settings-link' disabled={w.busy || role.unavailable} onClick={() => void edit(role)}>
            <View className='settings-copy'><Text>{role.name || tr("未命名角色")}</Text><Text className='settings-detail'>{role.preset ? tr("默认角色 · ") : ''}{roleRegionLabel(role.region)}{role.unavailable ? tr(" · 暂不可用") : ''}</Text></View>
            <View className='settings-chevron' ariaHidden />
          </Button>
          {role.unavailable && <Action disabled={w.busy} onClick={() => void w.refresh()}>{tr("重试加载")}</Action>}
          {role.id === active ? <Text className='settings-current'>{tr("✓ 当前使用")}</Text> : !role.unavailable && <Action disabled={w.busy} onClick={() => void useRole(role)}>{tr("使用角色")}</Action>}
        </View>)}
      </View>
      <Text className='settings-footnote'>{tr("点角色名称查看设置。切换角色仅对当前设备生效。")}</Text>
    </View>
  </View>;
}
