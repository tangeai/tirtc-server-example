import { useEffect, useRef, useState } from 'react';
import { Shell } from '../components/Shell';
import { useConfirm } from '../components/Confirm';
import { ApiError, errorText, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';
import { draftOf, editableProviders, matchingVoices, rolePayload, type Role, type RoleDraft, type Voice } from '../lib/roles';
import { useRemote } from '../lib/use-remote';
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
  const operation = useRef(false), selection = useRef(0), mounted = useRef(true), loadRevision = useRef(0);
  const voices = useRemote<{ items: Voice[] }>('/v1/ai/voices');
  const dirty = !!selected && !!draft && !selected.preset && JSON.stringify(draft) !== JSON.stringify(draftOf(selected));
  const effective = binding?.role_id || binding?.default_role_id;
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
    if (operation.current || role.unavailable || (!confirmed && !await discard())) return;
    const rev = ++selection.current; setLoading(true); setError('');
    try {
      const full = role.id && !role.preset ? { ...role, ...await request<Role>('/v1/ai/roles/' + encodeURIComponent(role.id)) } : role;
      if (!mounted.current || rev !== selection.current) return;
      setSelected(full); setDraft(draftOf(full)); setMobileEditor(true);
      if (full.id) setRoles(items => items.map(item => item.id === full.id ? full : item));
    } catch (e) { if (mounted.current && rev === selection.current) setError(errorText(e)); }
    finally { if (mounted.current && rev === selection.current) setLoading(false); }
  }
  async function save(): Promise<boolean> {
    if (resourceBlocked || !selected || !draft || selected.preset || operation.current) return false;
    let payload: Role;
    try { payload = rolePayload(selected, draft, voices.data?.items || []); }
    catch (e) { setError(e instanceof Error ? e.message : '保存失败'); return false; }
    operation.current = true; setBusy(true); setError('');
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
    operation.current = true; setBusy(true); setError('');
    try {
      await request('/v1/ai/device/' + encodeURIComponent(device) + '/role', { method: role.preset ? 'DELETE' : 'PUT', body: role.preset ? undefined : { role_id: role.id } });
      if (mounted.current) { setBinding(value => ({ default_role_id: value?.default_role_id || '', role_id: role.preset ? '' : role.id })); setNotice(t('已切换到“{p0}”', { p0: role.name || role.id })); switched = true; }
    } catch (e) { if (mounted.current) setError(errorText(e)); }
    finally { operation.current = false; if (mounted.current) setBusy(false); }
    if (switched && mounted.current) await select(role, true);
  }
  async function remove() {
    if (resourceBlocked || !selected?.id || selected.preset || operation.current) return;
    if (await ask({ title: '操作确认', message: '确定删除该角色？', choices: [{ value: 'cancel', label: '取消' }, { value: 'delete', label: '确认删除' }] }) !== 'delete') return;
    operation.current = true; setBusy(true); setError('');
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
  return <Shell title="AI 角色"><div className="role-context"><span>{name || t('未命名设备')} · {device}</span><span>{t('当前角色')} <strong>{current?.name || t(loading ? '正在加载…' : '角色不可用，请重新选择')}</strong></span></div>
    <div className={'roles-layout' + (mobileEditor ? ' show-editor' : '')}><aside className="role-list"><input type="search" value={search} onChange={e => setSearch(e.target.value)} placeholder={t('搜索智能体...')} aria-label={t('搜索智能体...')}/><p className="muted">{t('点击角色查看配置，切换仅影响当前设备。')}</p>
      {roles.filter(role => !search.trim() || [role.name, role.user_params?.description].some(value => value?.toLowerCase().includes(search.toLowerCase()))).map(role => <article key={role.id} className={'role-card' + (role.id === effective && !role.unavailable ? ' active-role' : selected?.id === role.id ? ' viewed-role' : '')}>
        <button className="role-preview" type="button" aria-label={role.name || role.id} aria-pressed={selected?.id === role.id} disabled={busy || resourceBlocked || role.unavailable} onClick={() => void select(role)}><span className="role-title" title={role.name || role.id}>{role.name || role.id}</span>
        <span className="muted" title={role.user_params?.description || ''}>{role.user_params?.description?.trim() || t('暂无描述')}</span></button>
        <div className="role-card-footer"><span className="badge">{t(role.preset ? '系统预设' : '我的角色')}</span>{role.id === effective && !role.unavailable ? <span className="active-badge">✓ {t('当前设备使用中')}</span> : !role.unavailable && <button className="switch-role" disabled={busy || resourceBlocked || loading} onClick={() => void switchTo(role)}>{t('切换到此角色')}</button>}</div>

      </article>)}
      {!roles.length && !loading && <p className="muted">{t('暂无角色，可新建自己的角色')}</p>}
      <button className="wide" disabled={busy || resourceBlocked} onClick={async () => { if (await discard()) await select({ id: '' }, true); }}>＋ {t('新建角色')}</button>
      {error && !selected && <><p role="alert" className="error">{t(error)}</p><button onClick={() => void initialize()}>{t('重新加载')}</button></>}
    </aside><main className="role-editor"><button className="mobile-back" onClick={() => setMobileEditor(false)}>← {t('返回角色列表')}</button>
      {error && <p role="alert" className="error">{t(error)}</p>}{notice && <p role="status" className="success">{t(notice)}</p>}{loading && <p role="status">{t('正在加载角色详情…')}</p>}
      {!loading && selected && draft ? <RoleEditor key={selected.id || 'new'} role={selected} draft={draft} setDraft={patch => setDraft(current => current ? { ...current, ...patch } : current)} voices={voices.data?.items || []} voiceLoading={voices.loading} voiceError={!!voices.error} retryVoices={voices.reload} busy={busy || loading} resourceBlocked={resourceBlocked} onResourceBlocked={setResourceBlocked} remove={remove}/> : !loading && <p className="muted">{t('选择或新建智能体')}</p>}
      {dirty && <div className="save-bar"><p>{t('保存角色会影响所有使用该角色的设备；应用到设备只切换当前设备。')}</p><div className="row"><button disabled={busy || resourceBlocked || loading} onClick={async () => { if (await discard() && selected) setDraft(draftOf(selected)); }}>{t('放弃修改')}</button><button className="primary" disabled={busy || resourceBlocked || loading} onClick={() => void save()}>{t(busy ? '保存中…' : '保存角色')}</button></div></div>}
    </main></div>
  </Shell>;
}

function RoleEditor({ role, draft, setDraft, voices, voiceLoading, voiceError, retryVoices, busy, resourceBlocked, onResourceBlocked, remove }: { role: Entry; draft: RoleDraft; setDraft(patch: Partial<RoleDraft>): void; voices: Voice[]; voiceLoading: boolean; voiceError: boolean; retryVoices(): void; busy: boolean; resourceBlocked: boolean; onResourceBlocked(value: boolean): void; remove(): Promise<void> }) {
  const { t } = useLanguage();
  const [section, setSection] = useState('basic');
  const [language, setLanguage] = useState(''), [playing, setPlaying] = useState(false), [audioError, setAudioError] = useState(false);
  const audio = useRef<HTMLAudioElement>(null), readonly = !!role.preset, providers = editableProviders(role);
  const patch = (value: Partial<RoleDraft>) => setDraft(value);
  const list = matchingVoices(voices, language, '');
  const voice = voices.find(v => v.id === draft.voice);
  useEffect(() => { const player = audio.current; if (player) { player.pause(); player.currentTime = 0; } setPlaying(false); setAudioError(false); return () => player?.pause(); }, [draft.voice]);
  return <section className="card role-form"><div className="role-form-heading">{role.avatar && /^https?:\/\//.test(role.avatar) ? <img src={role.avatar} alt={t('角色头像')}/> : <span aria-hidden="true">{role.avatar || 'AI'}</span>}<h1>{role.id ? t('正在查看：{p0}', { p0: role.name || role.id }) : t('新建角色')}</h1></div>{readonly && <p className="preset-notice">{t('这是系统预设，只能查看。需要修改时请新建自己的角色。')}</p>}
    <nav className="role-editor-nav" aria-label={t('角色设置')}>
      {[['basic', '基本信息'], ['voice', '声音'], ['advanced', '高级配置']].map(([value, label]) => <button key={value} type="button" aria-pressed={section === value} onClick={() => { audio.current?.pause(); setPlaying(false); setSection(value); }}>{t(label)}</button>)}
    </nav>
    <fieldset disabled={busy}>
      <div hidden={section !== 'basic'}>
      <label>{t('智能体名称')}<input required readOnly={readonly} value={draft.name} onChange={e => patch({ name: e.target.value })}/></label><label>{t('智能体描述')}<input readOnly={readonly} value={draft.description} onChange={e => patch({ description: e.target.value })}/></label><label>{t('角色设定')}<textarea aria-label={t('角色设定')} rows={5} readOnly={readonly} value={draft.prompt} onChange={e => patch({ prompt: e.target.value })}/></label><label>{t('开场白')}<input readOnly={readonly} value={draft.welcome} onChange={e => patch({ welcome: e.target.value })}/></label>
      </div>
      <section hidden={section !== 'voice'}><h2>{t('声音')}</h2><p className="muted">{t('选择声音后可试听，保存角色后生效。')}</p>{voiceError && <p className="error">{t('音色列表暂不可用，请稍后重试')} <button type="button" onClick={retryVoices}>{t('重试')}</button></p>}
        {!readonly && providers.tts && <><nav className="filter-tabs">{Object.entries({ '': '全部', zh: '中文', en: '英文（试听同中文）', ja: '日文', ko: '韩文' }).map(([value, label]) => <button type="button" key={value} aria-pressed={language === value} onClick={() => setLanguage(value)}>{t(label)}</button>)}</nav></>}
        <div className="voice-current"><span>{t('当前声音')}：<strong>{voice?.name || draft.voice || t('沿用基础配置')}</strong></span>
          {draft.voice && !readonly && providers.tts && <button type="button" onClick={() => patch({ voice: '' })}>{t('沿用基础配置')}</button>}
        </div>
        {voiceLoading && <p role="status">{t('正在加载声音列表…')}</p>}
        {!providers.tts && <p className="preset-notice">{t('此角色使用其他语音服务，声音配置保持原样。')}</p>}
        {!voiceLoading && !voiceError && !list.length && <p role="status" className="voice-empty">{t(voices.length ? '没有匹配的声音，当前选择保持不变' : '暂无可用声音，请稍后重试')} <button type="button" onClick={retryVoices}>{t('重新加载')}</button></p>}
        <label>{t('声音列表')}<select aria-label={t('声音列表')} value={draft.voice} disabled={readonly || !providers.tts || voiceLoading} onChange={event => patch({ voice: event.target.value })}>
          <option value="">{t('沿用基础配置')}</option>
          {draft.voice && !list.some(item => item.id === draft.voice) && <option value={draft.voice}>{voice?.name || draft.voice}</option>}
          {list.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
        </select></label>
        {voice?.sample_url && <button type="button" onClick={async () => { const player = audio.current; if (!player) return; if (playing) { player.pause(); setPlaying(false); } else { try { await player.play(); setPlaying(true); setAudioError(false); } catch { setAudioError(true); } } }}>{t(playing ? '暂停' : '试听')}</button>}
        {voice && !voice.sample_url && <p className="muted">{t('此声音暂无试听音频')}</p>}
        <audio ref={audio} preload="none" src={voice?.sample_url} onEnded={() => setPlaying(false)} onError={() => { setPlaying(false); setAudioError(true); }}/>{audioError && <p className="error">{t('试听失败，请重试')}</p>}
        <details><summary>{t('高级语音设置')}</summary><label>{t('语速')} · {draft.rate}<input type="range" min="0.5" max="2" step="0.1" disabled={readonly || !providers.tts} value={draft.rate} onChange={e => patch({ rate: Number(e.target.value) })}/></label><label>{t('音调')} · {draft.pitch}<input type="range" min="0.5" max="2" step="0.1" disabled={readonly || !providers.tts} value={draft.pitch} onChange={e => patch({ pitch: Number(e.target.value) })}/></label></details>
      </section>
      <div hidden={section !== 'advanced'}><details className="role-section"><summary>{t('模型与记忆')}</summary><label>{t('模型')}<select disabled={readonly || !providers.llm} value={draft.model} onChange={e => patch({ model: e.target.value })}><option value="">{t('沿用基础配置')}</option>{[...new Set(['qwen3.5-flash', 'qwen3.7-plus', draft.model])].filter(Boolean).map(model => <option key={model}>{model}</option>)}</select></label><label className="radio"><input type="checkbox" disabled={readonly} checked={draft.memory} onChange={e => patch({ memory: e.target.checked })}/>{t('长期记忆')}</label></details>
      <details className="role-section"><summary>{t('知识库')}</summary><Knowledge value={draft.knowledge} onChange={knowledge => patch({ knowledge })} readonly={readonly} onBlocked={onResourceBlocked}/></details>
      <details className="role-section"><summary>{t('工具与插件')}</summary><ResourceSelection kind="mcp" selected={draft.mcp} onChange={mcp => patch({ mcp })} readonly={readonly}/><ResourceSelection kind="plugin" selected={draft.plugins} onChange={plugins => patch({ plugins })} readonly={readonly} names={role.device_plugin_refs}/></details>
      </div><p className="muted role-edit-scope">{t('保存角色会影响所有使用该角色的设备；应用到设备只切换当前设备。')}</p>{role.id && !readonly && <button type="button" className="danger" disabled={resourceBlocked} onClick={() => void remove()}>{t('删除角色')}</button>}
    </fieldset>
  </section>;
}
