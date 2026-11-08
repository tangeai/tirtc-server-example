import { ConfigProvider, Select, Checkbox, Input } from 'antd';
import { Button } from '../../components/Button';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Dialog } from '../../components/Dialog';
import { useConfirm } from '../../components/Confirm';
import { errorText, request } from '../../lib/api';
import { useLanguage } from '../../lib/i18n';
import { useRemote } from '../../lib/use-remote';

interface Resource { id: string; name?: string; enabled?: boolean }
interface Param { name?: string; type: string; description?: string; default_value?: string; required?: boolean; [key: string]: unknown }
interface Plugin { name: string; description: string; action: string; input_params: Param[]; return_params: Param[] }
interface MCP { enabled: boolean; config: { name: string; description: string; url: string; authentication?: { type: string; bearer_token?: string } | null; [key: string]: unknown } }
const base = { mcp: '/v1/ai/mcp/app-tools', plugin: '/v1/ai/plugins' };

export function ResourceSelection({ kind, selected, onChange, readonly, names = [] }: { kind: 'mcp' | 'plugin'; selected: string[]; onChange(value: string[]): void; readonly: boolean; names?: Resource[] }) {
  const { t } = useLanguage(), ask = useConfirm();
  const own = useRemote<{ items: Resource[] }>(base[kind]);
  const global = useRemote<{ items: Resource[] }>(kind === 'mcp' ? '/v1/ai/mcp/tools' : undefined);
  const [editing, setEditing] = useState<{ id?: string }>(), [error, setError] = useState('');
  const pendingDelete = useRef(false), [deleting, setDeleting] = useState(false);
  const alive = useRef(true), selection = useRef(selected); selection.current = selected;
  useEffect(() => () => { alive.current = false; }, []);
  const catalog = [...(global.data?.items || []), ...(own.data?.items || []), ...names];
  const ids = [...new Set([...(readonly ? [] : catalog.map(item => item.id)), ...selected])];
  async function remove(id: string) {
    if (pendingDelete.current) return;
    pendingDelete.current = true;
    try {
      const confirmed = await ask({ title: '操作确认', message: kind === 'mcp' ? '删除前请在所有角色中解除该 MCP 工具的引用。确定删除？' : '删除前请先在引用该插件的角色中关闭它并保存角色。确定删除该插件？', choices: [{ value: 'cancel', label: '取消' }, { value: 'delete', label: '确认删除' }] });
      if (confirmed !== 'delete' || !alive.current) return;
      setDeleting(true); setError('');
      await request(base[kind] + '/' + encodeURIComponent(id), { method: 'DELETE' });
      if (alive.current) { onChange(selection.current.filter(value => value !== id)); own.reload(); }
    } catch (e) { if (alive.current) setError(errorText(e)); }
    finally { pendingDelete.current = false; if (alive.current) setDeleting(false); }
  }
  return <section className="resource-list"><h3>{t(kind === 'mcp' ? 'MCP 工具' : '设备插件')}</h3>
    {(own.error || global.error || error) && <p role="alert" className="error">{error || t('资源列表加载失败，请重试')} <Button type="button" onClick={() => { setError(''); own.reload(); global.reload(); }}>{t('重试')}</Button></p>}
    {ids.map(id => { const item = catalog.find(item => item.id === id), managed = own.data?.items?.some(item => item.id === id); return <div className="row" key={id}><Checkbox className="radio"  checked={selected.includes(id)} disabled={readonly || (!selected.includes(id) && item?.enabled === false)} onChange={e => onChange(e.target.checked ? [...selected, id] : selected.filter(value => value !== id))}><span>{item?.name || id}</span></Checkbox>{managed && !readonly && <div className="row"><Button type="button" onClick={() => setEditing({ id })}>{t('编辑')}</Button><Button type="button" className="danger" disabled={deleting} onClick={() => void remove(id)}>{t('删除')}</Button></div>}</div>; })}
    {!ids.length && <p className="muted">{t(own.loading ? '正在加载…' : kind === 'mcp' ? '未关联 MCP 工具' : '未关联设备插件')}</p>}
    {!readonly && <Button type="button" onClick={() => setEditing({})}>{t(kind === 'mcp' ? '+ 新建 MCP 工具' : '+ 新建设备插件')}</Button>}
    {editing && <ResourceEditor key={kind + (editing.id || 'new')} kind={kind} id={editing.id} close={() => setEditing(undefined)} saved={() => { own.reload(); setEditing(undefined); }}/>}
  </section>;
}
function ResourceEditor({ kind, id, close, saved }: { kind: 'mcp' | 'plugin'; id?: string; close(): void; saved(): void }) {
  const { t } = useLanguage(), ask = useConfirm();
  const remote = useRemote<MCP | Plugin>(id ? base[kind] + '/' + encodeURIComponent(id) : undefined);
  const [plugin, setPlugin] = useState<Plugin>({ name: '', description: '', action: '', input_params: [], return_params: [] });
  const [mcp, setMcp] = useState<MCP>({ enabled: true, config: { name: '', description: '', url: '', authentication: null } });
  const [busy, setBusy] = useState(false), [error, setError] = useState('');
  const pending = useRef(false), alive = useRef(true), closing = useRef(false);
  const baseline = useRef(JSON.stringify(kind === 'plugin' ? plugin : mcp));
  const dirty = JSON.stringify(kind === 'plugin' ? plugin : mcp) !== baseline.current;
  async function dismiss() {
    if (pending.current || closing.current) return;
    if (!dirty) { close(); return; }
    closing.current = true;
    try {
      const answer = await ask({ title: '修改尚未保存', message: '放弃当前资源的修改？', choices: [{ value: 'cancel', label: '继续编辑' }, { value: 'discard', label: '放弃修改' }] });
      if (answer === 'discard' && alive.current) close();
    } finally { closing.current = false; }
  }
  useEffect(() => {
    const before = (event: BeforeUnloadEvent) => { if (dirty) { event.preventDefault(); event.returnValue = ''; } };
    window.addEventListener('beforeunload', before); return () => window.removeEventListener('beforeunload', before);
  }, [dirty]);
  useEffect(() => () => { alive.current = false; }, []);
  useEffect(() => {
    if (!remote.data) return;
    if ('config' in remote.data) {
      const value = { ...remote.data, config: { ...remote.data.config, description: remote.data.config.description ?? '' } };
      baseline.current = JSON.stringify(value); setMcp(value);
    } else {
      const value = { ...remote.data, description: remote.data.description ?? '', input_params: remote.data.input_params || [], return_params: remote.data.return_params || [] };
      baseline.current = JSON.stringify(value); setPlugin(value);
    }
  }, [remote.data]);
  const mc = (patch: Partial<MCP['config']>) => setMcp(value => ({ ...value, config: { ...value.config, ...patch } }));
  async function submit(event: FormEvent) {
    event.preventDefault(); if (pending.current) return;
    const clean = (rows: Param[]) => rows.map(p => ({ ...p, ...(typeof p.name === 'string' ? { name: p.name.trim() } : {}), ...(typeof p.description === 'string' ? { description: p.description.trim() } : {}) }));
    const payload = kind === 'mcp' ? { ...mcp, config: { ...mcp.config, name: mcp.config.name.trim(), url: mcp.config.url.trim(), description: mcp.config.description.trim() } } : { ...plugin, name: plugin.name.trim(), action: plugin.action.trim(), description: plugin.description.trim(), input_params: clean(plugin.input_params), return_params: clean(plugin.return_params) };
    pending.current = true; setBusy(true); setError('');
    try { await request(base[kind] + (id ? '/' + encodeURIComponent(id) : ''), { method: id ? 'PUT' : 'POST', body: payload }); if (alive.current) saved(); }
    catch (e) { if (alive.current) setError(errorText(e)); }
    finally { pending.current = false; if (alive.current) setBusy(false); }
  }
  return <Dialog title={kind === 'mcp' ? id ? '编辑 MCP 工具' : '新增 MCP 工具' : id ? '编辑设备端插件' : '新增设备端插件'} onClose={() => void dismiss()} closeDisabled={busy} size={kind === 'plugin' ? 'wide' : undefined}>
    {remote.loading && <p role="status">{t('正在加载…')}</p>}{remote.error ? <p role="alert" className="error">{errorText(remote.error)} <Button onClick={remote.reload}>{t('重试')}</Button></p> : (!id || remote.data) && <form className={kind === 'plugin' ? 'plugin-editor' : undefined} onSubmit={e => void submit(e)}><ConfigProvider componentDisabled={busy}><fieldset disabled={busy}>
      <label>{t('名称')}<Input required value={kind === 'mcp' ? mcp.config.name : plugin.name} onChange={e => kind === 'mcp' ? mc({ name: e.target.value }) : setPlugin({ ...plugin, name: e.target.value })}/></label>
      <label>{t('描述')}<Input.TextArea rows={2} value={kind === 'mcp' ? mcp.config.description : plugin.description} onChange={e => kind === 'mcp' ? mc({ description: e.target.value }) : setPlugin({ ...plugin, description: e.target.value })}/></label>
      {kind === 'mcp' ? <><label>{t('MCP 服务 URL')}<Input type="url" required value={mcp.config.url} onChange={e => mc({ url: e.target.value })}/></label><label>{t('认证方式')}<Select aria-label={t('认证方式')} value={mcp.config.authentication?.type || ''} onChange={value => mc({ authentication: value ? { type:value, bearer_token:'' } : null })} options={[{value:'',label:t('无')},{value:'bearer',label:'Bearer Token'}, ...(mcp.config.authentication?.type && mcp.config.authentication.type !== 'bearer' ? [{value:mcp.config.authentication.type,label:mcp.config.authentication.type}] : [])]}/></label>{mcp.config.authentication?.type === 'bearer' && <label>Bearer Token<Input type="password" autoComplete="off" value={mcp.config.authentication.bearer_token || ''} onChange={e => mc({ authentication: { ...mcp.config.authentication!, bearer_token: e.target.value } })}/></label>}<Checkbox className="radio"  checked={mcp.enabled} onChange={e => setMcp({ ...mcp, enabled: e.target.checked })}>{t('启用')}</Checkbox></> : <><label>{t('功能标识（Action）')}<Input required value={plugin.action} onChange={e => setPlugin({ ...plugin, action: e.target.value })}/></label><Params title="输入参数" rows={plugin.input_params} required onChange={rows => setPlugin({ ...plugin, input_params: rows })}/><Params title="返回参数" rows={plugin.return_params} onChange={rows => setPlugin({ ...plugin, return_params: rows })}/></>}
      {error && <p role="alert" className="error">{error}</p>}<div className={kind === 'plugin' ? 'plugin-editor-footer' : undefined}>{kind === 'plugin' && <Button type="button" onClick={() => void dismiss()}>{t('取消')}</Button>}<Button className={kind === 'plugin' ? 'primary' : 'primary wide'}>{t(busy ? '保存中…' : '保存')}</Button></div>
    </fieldset></ConfigProvider></form>}
  </Dialog>;
}
function Params({ title, rows, required, onChange }: { title: string; rows: Param[]; required?: boolean; onChange(rows: Param[]): void }) {
  const { t } = useLanguage();
  const container = useRef<HTMLElement>(null), keys = useRef<number[]>([]), serial = useRef(0), focusAdded = useRef(false);
  while (keys.current.length < rows.length) keys.current.push(++serial.current);
  useEffect(() => {
    if (!focusAdded.current) return;
    focusAdded.current = false;
    const inputs = container.current?.querySelectorAll<HTMLInputElement>('.param-name input');
    const last = inputs?.[inputs.length - 1]; last?.focus(); last?.scrollIntoView({ block: 'nearest' });
  }, [rows.length]);
  const patch = (index: number, value: Partial<Param>) => onChange(rows.map((row, i) => i === index ? { ...row, ...value } : row));
  return <section ref={container} className={'parameter-table' + (required ? '' : ' return-parameters')}>
    <div className="parameter-toolbar"><h3>{t(title)} <small>({rows.length})</small></h3><Button type="button" onClick={() => { focusAdded.current = true; onChange([...rows, { name: '', type: 'string', description: '', default_value: '', required: false }]); }}>{t('添加参数')}</Button></div>
    {!rows.length ? <p className="muted parameter-empty">{t('暂无参数')}</p> : <div className="parameter-columns" aria-hidden="true"><span>{t('参数名称')}</span><span>{t('类型')}</span><span>{t('参数说明')}</span><span>{t('默认值')}</span>{required && <span>{t('必填')}</span>}<span/></div>}
    {rows.map((row, index) => <ParamRow key={keys.current[index]} row={row} required={required} position={index + 1} title={title} patch={value => patch(index, value)} remove={() => { keys.current.splice(index, 1); onChange(rows.filter((_, i) => i !== index)); }}/>) }
  </section>;
}
function ParamRow({ row, required, position, title, patch, remove }: { row: Param; required?: boolean; position: number; title: string; patch(value: Partial<Param>): void; remove(): void }) {
  const { t } = useLanguage();
  const [expanded, setExpanded] = useState(false);
  return <fieldset className={'param-card' + (expanded ? ' expanded' : '')} aria-label={`${t(title)} ${position}`}>
    <label className="param-name"><span>{t('参数名称')}</span><Input aria-label={t('参数名称')} value={row.name ?? ''} pattern="[A-Za-z0-9_]+" placeholder="parameter_name" onChange={e => patch({ name: e.target.value })}/></label>
    <label className="param-type"><span>{t('类型')}</span><Select aria-label={t('类型')} value={row.type} onChange={type => patch({type})} options={[...new Set(['string','integer','boolean','array','object',row.type])].map(value=>({value,label:value}))}/></label>
    <label className="param-description"><span>{t('参数说明')}</span><Input aria-label={t('参数说明')} value={row.description ?? ''} onChange={e => patch({ description: e.target.value })}/></label>
    <label className="param-default"><span>{t('默认值')}</span><Input aria-label={t('默认值')} value={row.default_value ?? ''} onChange={e => patch({ default_value: e.target.value })}/></label>
    {required && <Checkbox className="radio param-required" aria-label={t('必填')}  checked={!!row.required} onChange={e => patch({ required: e.target.checked })}><span>{t('必填')}</span></Checkbox>}
    <Button className="danger param-remove" aria-label={t('删除参数')} title={t('删除参数')} type="button" onClick={remove}>×</Button>
    <Button className="param-expand" type="button" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{t(expanded ? '收起详情' : '说明与默认值')}</Button>
  </fieldset>;
}
