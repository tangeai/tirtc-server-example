import { useEffect, useRef, useState } from 'react';
import { Conversations } from '../components/Conversations';
import { Shell } from '../components/Shell';
import { useConfirm } from '../components/Confirm';
import { ApiError, errorText, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';
import { draftOf, editableProviders, llmModels, rolePayload, type Role, type RoleDraft, type Voice } from '../lib/roles';
import { useRemote } from '../lib/use-remote';
import { RoleVoicePanel } from './roles/RoleVoicePanel';
import { Knowledge } from './roles/Knowledge';
import { ResourceSelection } from './roles/Resources';

interface Entry extends Role { id: string; preset?: boolean; unavailable?: boolean }
interface Binding { role_id: string; default_role_id: string }
export default function Roles() {
  const { t } = useLanguage(), ask = useConfirm();
  const device = new URLSearchParams(location.search).get('device_id') ?? '';
  const [roles, setRoles] = useState<Entry[]>([]), [binding, setBinding] = useState<Binding>(), [name, setName] = useState('');
  const [selected, setSelected] = useState<Entry>(), [draft, setDraft] = useState<RoleDraft>(), [search, setSearch] = useState('');
  const [loading, setLoading] = useState(true), [busy, setBusy] = useState(false), [error, setError] = useState(''), [notice, setNotice] = useState(''), [mobileEditor, setMobileEditor] = useState(false);
  const [resourceBlocked, setResourceBlocked] = useState(false);
  const [history, setHistory] = useState(false);
  const operation = useRef(false), selection = useRef(0), mounted = useRef(true), loadRevision = useRef(0);
  const voices = useRemote<{ items: Voice[] }>('/v1/ai/voices');
  const dirty = !!selected && !!draft && !selected.preset && JSON.stringify(draft) !== JSON.stringify(draftOf(selected));
  const effective = binding?.role_id || binding?.default_role_id;
  const filteredRoles = roles.filter(role => !search.trim() || [role.name, role.user_params?.description].some(value => value?.toLowerCase().includes(search.trim().toLowerCase())));
  const current = roles.find(role => role.id === effective && !role.unavailable);
  async function initialize() {
    const rev = ++loadRevision.current; setLoading(true); setError('');
    try {
      if (!device) { setError('未指定设备'); return; }
      const [devices, owned, assignment] = await Promise.all([request<{ device_id: string; device_name: string }[]>('/v1/user/device/list'), request<{ items: Entry[] }>('/v1/ai/roles'), request<Binding>('/v1/ai/device/' + encodeURIComponent(device) + '/role')]);
      if (!mounted.current || rev !== loadRevision.current) return;
      const d = devices.find(item => item.device_id === device); if (!d) { setError('设备不属于当前账号，请返回设备列表'); return; }
      setName(d.device_name); setBinding(assignment);
      let list = owned.items || [];
      if (assignment.default_role_id) {
        let preset: Entry;
        try { preset = { ...await request<Role>('/v1/ai/roles/default'), id: assignment.default_role_id, preset: true }; }
        catch { preset = { id: assignment.default_role_id, name: t('默认智能体'), preset: true, unavailable: true }; }
        list = [preset, ...list.filter(role => role.id !== preset.id)];
      }
      const active = assignment.role_id || assignment.default_role_id;
      // A paginated list can omit the currently bound role. Fetch its detail
      // before calling it unavailable; never silently rebind the device.
      if (active && !list.some(role => role.id === active && !role.unavailable)) {
        try { const role = await request<Role>(active === assignment.default_role_id ? '/v1/ai/roles/default' : '/v1/ai/roles/' + encodeURIComponent(active)); list = [...list.filter(r => r.id !== active), { ...role, id: active, preset: active === assignment.default_role_id }]; }
        catch { /* Known alternatives remain viewable while the current role is unavailable. */ }
      }
      if (!mounted.current || rev !== loadRevision.current) return;
      setRoles(list);
      const preview = list.find(role => role.id === active && !role.unavailable) || list.find(role => !role.unavailable);
      if (preview) await select(preview, true);
    } catch (e) { if (mounted.current && rev === loadRevision.current) setError(errorText(e)); }
    finally { if (mounted.current && rev === loadRevision.current) setLoading(false); }
  }
  useEffect(() => { void initialize(); return () => { mounted.current = false; selection.current++; loadRevision.current++; }; }, [device]);
  useEffect(() => {
    const before = (event: BeforeUnloadEvent) => { if (dirty || resourceBlocked) { event.preventDefault(); event.returnValue = ''; } };
    window.addEventListener('beforeunload', before); return () => window.removeEventListener('beforeunload', before);
  }, [dirty, resourceBlocked]);
  async function discard() {
    if (resourceBlocked) return false;
    if (!dirty) return true;
    return await ask({ title: '修改尚未保存', message: '放弃当前角色的修改？', choices: [{ value: 'cancel', label: '继续编辑' }, { value: 'discard', label: '放弃修改' }] }) === 'discard';
  }
  async function select(role: Entry, confirmed = false) {
    if (operation.current || role.unavailable) return;
    if (!confirmed && role.id && selected?.id === role.id && draft) { selection.current++; setLoading(false); setMobileEditor(true); return; }
    if (!confirmed && !await discard()) return;
    const rev = ++selection.current; setLoading(true); setError(''); setNotice('');
    try {
      const full = role.id && !role.preset ? { ...role, ...await request<Role>('/v1/ai/roles/' + encodeURIComponent(role.id)) } : role;
      if (!mounted.current || rev !== selection.current) return;
      setSelected(full); setDraft(draftOf(full)); setMobileEditor(true);
      if (!full.id) setSearch('');
      if (full.id) setRoles(items => items.map(item => item.id === full.id ? full : item));
    } catch (e) { if (mounted.current && rev === selection.current) setError(errorText(e)); }
    finally { if (mounted.current && rev === selection.current) setLoading(false); }
  }
  async function save(): Promise<boolean> {
    if (resourceBlocked || !selected || !draft || selected.preset || operation.current) return false;
    let payload: Role;
    try { payload = rolePayload(selected, draft, voices.data?.items || []); }
    catch (e) { setError(e instanceof Error ? e.message : '保存失败'); return false; }
    operation.current = true; setBusy(true); setError(''); setNotice('');
    try {
      const result = await request<Role>('/v1/ai/roles' + (selected.id ? '/' + encodeURIComponent(selected.id) : ''), { method: selected.id ? 'PUT' : 'POST', body: payload });
      if (!mounted.current) return false;
      const id = result?.id || selected.id;
      if (!id) { setError('保存结果暂不可用，请重新加载角色列表'); return false; }
      const saved: Entry = { ...selected, ...payload, ...result, id };
      setRoles(items => items.some(role => role.id === id) ? items.map(role => role.id === id ? saved : role) : [...items, saved]);
      setSelected(saved); setDraft(draftOf(saved)); setNotice('保存成功'); return true;
    } catch (e) { if (mounted.current) setError(errorText(e)); return false; }
    finally { operation.current = false; if (mounted.current) setBusy(false); }
  }
  async function switchTo(role: Entry) {
    if (resourceBlocked || operation.current || loading || role.unavailable || role.id === effective) return;
    if (dirty) {
      const answer = await ask({ title: '修改尚未保存', message: t('切换到“{p0}”前，请处理当前角色的修改。', { p0: role.name || role.id }), choices: [{ value: 'save', label: '保存并切换' }, { value: 'discard', label: '放弃修改并切换' }, { value: 'cancel', label: '继续编辑' }] });
      if (answer === 'cancel' || (answer === 'save' && !await save())) return;
      if (answer === 'discard' && selected) setDraft(draftOf(selected));
    }
    let switched = false;
    operation.current = true; setBusy(true); setError(''); setNotice('');
    try {
      await request('/v1/ai/device/' + encodeURIComponent(device) + '/role', { method: role.preset ? 'DELETE' : 'PUT', body: role.preset ? undefined : { role_id: role.id } });
      if (mounted.current) { setBinding(value => ({ default_role_id: value?.default_role_id || '', role_id: role.preset ? '' : role.id })); switched = true; }
    } catch (e) { if (mounted.current) setError(errorText(e)); }
    finally { operation.current = false; if (mounted.current) setBusy(false); }
    if (switched && mounted.current) { await select(role, true); if (mounted.current) setNotice(t('已切换到“{p0}”', { p0: role.name || role.id })); }
  }
  async function remove() {
    if (resourceBlocked || !selected?.id || selected.preset || operation.current) return;
    if (await ask({ title: '操作确认', message: '确定删除该角色？', choices: [{ value: 'cancel', label: '取消' }, { value: 'delete', label: '确认删除' }] }) !== 'delete') return;
    operation.current = true; setBusy(true); setError(''); setNotice('');
    try {
      await request('/v1/ai/roles/' + encodeURIComponent(selected.id), { method: 'DELETE' });
      if (mounted.current) { setRoles(items => items.filter(item => item.id !== selected.id)); setSelected(undefined); setDraft(undefined); setMobileEditor(false); }
    } catch (e) {
      if (!mounted.current) return;
      if (e instanceof ApiError && e.code === 40900) {
        const data = e.data as { devices?: { device_id: string; device_name?: string }[] } | undefined;
        const lines = Array.isArray(data?.devices) ? data.devices.map(d => `${d.device_name || t('未命名设备')} (${d.device_id})`) : [];
        await ask({ title: '暂时无法删除角色', message: [e.message, ...lines].join('\n'), choices: [{ value: 'ok', label: '知道了' }] });
      } else setError(errorText(e));
    } finally { operation.current = false; if (mounted.current) setBusy(false); }
  }
  return <div className="roles-page"><Shell title="AI 角色"><div className="role-context"><span>{name || t('未命名设备')} · {device}</span><span>{t('当前角色')} <strong>{current?.name || t(loading ? '正在加载…' : '角色不可用，请重新选择')}</strong></span><button className="role-history" disabled={!device || !binding} onClick={() => setHistory(true)}>{t('会话记录')}</button></div>
    {history && <Conversations device={device} name={name || t('未命名设备')} close={() => setHistory(false)}/>}
    <div className={'roles-layout' + (mobileEditor ? ' show-editor' : '')}><aside className="role-list"><input type="search" value={search} onChange={e => setSearch(e.target.value)} placeholder={t('搜索智能体...')} aria-label={t('搜索智能体...')}/><p className="muted">{t('点击角色查看配置，切换仅影响当前设备。')}</p>
      {filteredRoles.map(role => <article key={role.id} className={'role-card' + (role.id === effective && !role.unavailable ? ' active-role' : selected?.id === role.id ? ' viewed-role' : '')}>
        <button className="role-preview" type="button" aria-label={role.name || role.id} aria-pressed={selected?.id === role.id} disabled={busy || resourceBlocked || role.unavailable} onClick={() => void select(role)}><span className="role-title" title={role.name || role.id}>{role.name || role.id}</span>
        <span className="muted" title={role.user_params?.description || ''}>{role.user_params?.description?.trim() || t('暂无描述')}</span></button>
        <div className="role-card-footer"><span className="badge">{t(role.preset ? '系统预设' : '我的角色')}</span>{role.id === effective && !role.unavailable ? <span className="active-badge">✓ {t('当前设备使用中')}</span> : !role.unavailable && <button className="switch-role" disabled={busy || resourceBlocked || loading} onClick={() => void switchTo(role)}>{t('切换到此角色')}</button>}</div>

      </article>)}
      {!!roles.length && !filteredRoles.length && !loading && <p role="status" className="muted">{t('没有匹配的角色，请换个关键词。')}</p>}
      {!roles.length && !loading && <p className="muted">{t('暂无角色，可新建自己的角色')}</p>}
      <button className="wide" disabled={busy || resourceBlocked} onClick={async () => { if (await discard()) await select({ id: '' }, true); }}>＋ {t('新建角色')}</button>
      {error && !selected && <><p role="alert" className="error">{t(error)}</p><button onClick={() => void initialize()}>{t('重新加载')}</button></>}
    </aside><main className="role-editor"><button className="mobile-back" onClick={() => setMobileEditor(false)}>← {t('返回角色列表')}</button>
      {error && <p role="alert" className="error">{t(error)}</p>}{notice && <p role="status" className="success">{t(notice)}</p>}{loading && <p role="status">{t('正在加载角色详情…')}</p>}
      {!loading && selected && draft ? <RoleEditor key={selected.id || 'new'} role={selected} draft={draft} setDraft={patch => { setNotice(''); setDraft(current => current ? { ...current, ...patch } : current); }} voices={voices.data?.items || []} voiceLoading={voices.loading} voiceError={!!voices.error} retryVoices={voices.reload} busy={busy || loading} resourceBlocked={resourceBlocked} onResourceBlocked={setResourceBlocked} remove={remove}/> : !loading && <p className="muted">{t('选择或新建智能体')}</p>}
      {selected && !selected.preset && <div className="save-bar"><p>{t('保存会影响所有使用该角色的设备；“切换到此角色”只更换当前设备。')}</p><div className="row"><button disabled={!dirty || busy || resourceBlocked || loading} onClick={async () => { if (await discard() && selected) setDraft(draftOf(selected)); }}>{t('放弃修改')}</button><button className="primary" disabled={!dirty || busy || resourceBlocked || loading} onClick={() => void save()}>{t(busy ? '保存中…' : '保存角色')}</button></div></div>}
    </main></div>
  </Shell></div>;
}

function RoleEditor({ role, draft, setDraft, voices, voiceLoading, voiceError, retryVoices, busy, resourceBlocked, onResourceBlocked, remove }: { role: Entry; draft: RoleDraft; setDraft(patch: Partial<RoleDraft>): void; voices: Voice[]; voiceLoading: boolean; voiceError: boolean; retryVoices(): void; busy: boolean; resourceBlocked: boolean; onResourceBlocked(value: boolean): void; remove(): Promise<void> }) {
  const { t } = useLanguage();
  const [section, setSection] = useState('basic');
  const readonly = !!role.preset, providers = editableProviders(role);
  const patch = (value: Partial<RoleDraft>) => setDraft(value);
  return <section className="card role-form"><div className="role-form-heading">{role.avatar && /^https?:\/\//.test(role.avatar) ? <img src={role.avatar} alt={t('角色头像')}/> : <span aria-hidden="true">{role.avatar || 'AI'}</span>}<h1>{role.id ? t('正在查看：{p0}', { p0: role.name || role.id }) : t('新建角色')}</h1></div>{readonly && <p className="preset-notice">{t('这是系统预设，只能查看。需要修改时请新建自己的角色。')}</p>}
    <nav className="role-editor-nav" aria-label={t('角色设置')}>
      {[['basic', '角色'], ['model', '模型与记忆'], ['advanced', '扩展能力']].map(([value, label]) => <button key={value} type="button" aria-pressed={section === value} onClick={() => setSection(value)}>{t(label)}</button>)}
    </nav>
    <fieldset disabled={busy}>
      <div className="role-basic" hidden={section !== 'basic'}>
        <label>{t('智能体名称')}<input required readOnly={readonly} value={draft.name} onChange={e => patch({ name: e.target.value })}/></label>
        <RoleVoicePanel role={role} draft={draft} patch={patch} voices={voices} voiceLoading={voiceLoading} voiceError={voiceError} retryVoices={retryVoices} busy={busy} active={section === 'basic'} readonly={readonly}/>

      </div>
      <div hidden={section !== 'model'}><section className="role-section"><h2>{t('模型与记忆')}</h2><label>{t('模型')}<select aria-label={t('模型')} disabled={readonly || !providers.llm} value={draft.model} onChange={e => patch({ model: e.target.value })}><option value="">{t('沿用基础配置')}</option>{[...new Set([...llmModels.map(model => model.id), draft.model])].filter(Boolean).map(model => <option key={model}>{model}</option>)}</select></label><label className="radio"><input type="checkbox" disabled={readonly} checked={draft.memory} onChange={e => patch({ memory: e.target.checked })}/>{t('长期记忆')}</label></section></div>
      <div hidden={section !== 'advanced'}>
        <details className="role-section"><summary>{t('更多角色信息')}</summary><label>{t('智能体描述')}<input readOnly={readonly} value={draft.description} onChange={e => patch({ description: e.target.value })}/></label><label>{t('开场白')}<input readOnly={readonly} value={draft.welcome} onChange={e => patch({ welcome: e.target.value })}/></label></details>
      <details className="role-section"><summary>{t('知识库')}</summary><Knowledge value={draft.knowledge} onChange={knowledge => patch({ knowledge })} readonly={readonly} onBlocked={onResourceBlocked}/></details>
      <details className="role-section"><summary>{t('工具与插件')}</summary><ResourceSelection kind="mcp" selected={draft.mcp} onChange={mcp => patch({ mcp })} readonly={readonly}/><ResourceSelection kind="plugin" selected={draft.plugins} onChange={plugins => patch({ plugins })} readonly={readonly} names={role.device_plugin_refs}/></details>
      {role.id && !readonly && <button type="button" className="danger" disabled={resourceBlocked} onClick={() => void remove()}>{t('删除角色')}</button>}</div>
    </fieldset>
  </section>;
}
