import { ConfigProvider, Select, Upload, Input } from 'antd';
import { Button } from '../../components/Button';
import { useEffect, useMemo, useRef, useState, useSyncExternalStore, type FormEvent } from 'react';
import { Dialog } from '../../components/Dialog';
import { useConfirm } from '../../components/Confirm';
import { errorText, request } from '../../lib/api';
import { useLanguage } from '../../lib/i18n';
import { KnowledgeDocuments, type KnowledgeDocument } from '../../lib/knowledge-documents';
import { useRemote } from '../../lib/use-remote';
interface Index { id: string; name: string; description: string; document_ids?: string[] }
const path = '/v1/ai/knowledge/indexes';

export function Knowledge({ value, onChange, readonly, onBlocked }: { value: string; onChange(value: string): void; readonly: boolean; onBlocked?(value: boolean): void }) {
  const { t } = useLanguage(), ask = useConfirm();
  const catalog = useRemote<{ items: Index[] }>(path + '?page_size=100');
  const [editing, setEditing] = useState<{ id?: string }>(), [error, setError] = useState('');
  const [blocked, setBlocked] = useState(false);
  useEffect(() => { onBlocked?.(blocked); return () => onBlocked?.(false); }, [blocked, onBlocked]);
  const selection = useRef(value); selection.current = value;
  const pendingDelete = useRef(false), [deleting, setDeleting] = useState(false);
  const alive = useRef(true); useEffect(() => () => { alive.current = false; }, []);
  async function remove() {
    if (pendingDelete.current || !value) return;
    pendingDelete.current = true;
    const target = value;
    try {
      if (await ask({ title: '操作确认', message: '确定删除该知识库？已绑定此知识库的智能体将失去检索能力。', choices: [{ value: 'cancel', label: '取消' }, { value: 'delete', label: '确认删除' }] }) !== 'delete' || !alive.current) return;
      setDeleting(true); setError('');
      await request(path + '/' + encodeURIComponent(target), { method: 'DELETE' });
      if (alive.current) { if (selection.current === target) onChange(''); catalog.reload(); }
    } catch (e) { if (alive.current) setError(errorText(e)); }
    finally { pendingDelete.current = false; if (alive.current) setDeleting(false); }
  }
  return <section><label>{t('知识库')}<Select aria-label={t('知识库')} disabled={readonly || blocked} value={value} onChange={onChange} options={[{value:'',label:t('未选择')}, ...(value && !catalog.data?.items?.some(i => i.id === value) ? [{value,label:value}] : []), ...(catalog.data?.items || []).map(i=>({value:i.id,label:i.name}))]}/></label>
    {(catalog.error || error) && <p className="error" role="alert">{error || errorText(catalog.error)} <Button type="button" onClick={() => { setError(''); catalog.reload(); }}>{t('重试')}</Button></p>}
    {!readonly && <div className="knowledge-actions"><Button type="button" disabled={blocked} onClick={() => setEditing({})}>{t('创建知识库')}</Button><Button type="button" disabled={!value || blocked} onClick={() => setEditing({ id: value })}>{t('编辑知识库')}</Button><Button type="button" className="danger" disabled={!value || deleting || blocked} onClick={() => void remove()}>{t('删除')}</Button></div>}
    {value && <Documents key={'documents:' + value} index={value} readonly={readonly} onBlocked={setBlocked}/>}
    {editing && <IndexEditor key={'editor:' + (editing.id || 'new')} id={editing.id} close={() => setEditing(undefined)} saved={id => { onChange(id); catalog.reload(); setEditing(undefined); }}/>}
  </section>;
}
function IndexEditor({ id, close, saved }: { id?: string; close(): void; saved(id: string): void }) {
  const { t } = useLanguage(), ask = useConfirm();
  const remote = useRemote<Index>(id ? path + '/' + encodeURIComponent(id) : undefined);
  const [name, setName] = useState(''), [description, setDescription] = useState(''), [busy, setBusy] = useState(false), [error, setError] = useState('');
  const alive = useRef(true), pending = useRef(false); useEffect(() => () => { alive.current = false; }, []);
  const baseline = useRef({ name: '', description: '' }), closing = useRef(false);
  const dirty = name !== baseline.current.name || description !== baseline.current.description;
  useEffect(() => {
    if (remote.data) {
      baseline.current = { name: remote.data.name || '', description: remote.data.description || '' };
      setName(baseline.current.name); setDescription(baseline.current.description);
    }
  }, [remote.data]);
  useEffect(() => {
    const before = (event: BeforeUnloadEvent) => { if (dirty) { event.preventDefault(); event.returnValue = ''; } };
    window.addEventListener('beforeunload', before); return () => window.removeEventListener('beforeunload', before);
  }, [dirty]);
  async function dismiss() {
    if (pending.current || closing.current) return;
    if (!dirty) { close(); return; }
    closing.current = true;
    try {
      const answer = await ask({ title: '修改尚未保存', message: '放弃当前资源的修改？', choices: [{ value: 'cancel', label: '继续编辑' }, { value: 'discard', label: '放弃修改' }] });
      if (answer === 'discard' && alive.current) close();
    } finally { closing.current = false; }
  }
  async function submit(event: FormEvent) {
    event.preventDefault(); if (pending.current) return;
    if (!name.trim() || !description.trim()) { setError('请输入知识库名称和描述'); return; }
    pending.current = true; setBusy(true); setError('');
    try { const data = await request<{ index_id: string }>(path + (id ? '/' + encodeURIComponent(id) : ''), { method: id ? 'PUT' : 'POST', body: { name: name.trim(), description: description.trim() } }); if (alive.current) saved(id || data.index_id); }
    catch (e) { if (alive.current) setError(errorText(e)); }
    finally { pending.current = false; if (alive.current) setBusy(false); }
  }
  return <Dialog title={id ? '编辑知识库' : '创建知识库'} onClose={() => void dismiss()} closeDisabled={busy}>{remote.loading && <p role="status">{t('正在加载…')}</p>}{remote.error ? <p className="error" role="alert">{errorText(remote.error)} <Button onClick={remote.reload}>{t('重试')}</Button></p> : (!id || remote.data) && <form onSubmit={e => void submit(e)}><ConfigProvider componentDisabled={busy}><fieldset disabled={busy}><label>{t('知识库名称')}<Input required value={name} onChange={e => setName(e.target.value)}/></label><label>{t('知识库描述')}<Input.TextArea required value={description} onChange={e => setDescription(e.target.value)}/></label>{error && <p role="alert" className="error">{error}</p>}<Button className="primary wide">{t(busy ? '保存中…' : '保存')}</Button></fieldset></ConfigProvider></form>}</Dialog>;
}
function Documents({ index, readonly, onBlocked }: { index: string; readonly: boolean; onBlocked(value: boolean): void }) {
  const { t } = useLanguage(), ask = useConfirm();
  const model = useMemo(() => new KnowledgeDocuments(index, request), [index]);
  const state = useSyncExternalStore(model.subscribe, model.snapshot);
  useEffect(() => { onBlocked(state.busy || state.pending); return () => onBlocked(false); }, [state.busy, state.pending, onBlocked]);
  useEffect(() => { void model.refresh(); return () => model.dispose(); }, [model]);
  useEffect(() => {
    const before = (event: BeforeUnloadEvent) => { if (state.busy || state.pending) { event.preventDefault(); event.returnValue = ''; } };
    window.addEventListener('beforeunload', before); return () => window.removeEventListener('beforeunload', before);
  }, [state.busy, state.pending]);
  async function remove(document: KnowledgeDocument, destroy: boolean) {
    const accepted = await ask({ title: '操作确认', message: destroy ? '确定删除该文件？' : '确定从该知识库移除该文件？文件本身保留在文件池。', choices: [{ value: 'cancel', label: '取消' }, { value: 'delete', label: destroy ? '确认删除' : '移除' }] });
    if (accepted === 'delete') await model.remove(document, destroy);
  }
  return <section><div className="row"><h3>{t('关联文件')}</h3><Button type="button" disabled={state.busy} onClick={() => void model.refresh()}>{t('刷新文件列表')}</Button></div>
    {state.loading && <p role="status">{t('正在加载…')}</p>}
    {state.notice && <p role="status">{t(state.notice)}</p>}
    {!!state.error && <p className="error" role="alert">{state.errorLabel ? t(state.errorLabel) : errorText(state.error)} {!state.retryAssociation && <Button type="button" disabled={state.busy} onClick={() => void model.refresh()}>{t('重试')}</Button>}</p>}
    {state.retryAssociation && !state.busy && <><Button type="button" onClick={() => void model.retryAssociation()}>{t('重试关联')}</Button><Button type="button" onClick={async () => { if (await ask({ title: '操作确认', message: '确定删除该文件？', choices: [{ value: 'cancel', label: '取消' }, { value: 'delete', label: '确认删除' }] }) === 'delete') await model.discardUpload(); }}>{t('删除已上传文件')}</Button></>}
    {state.documents.map(document => <div className="file-row" key={document.document_id}><span>{document.name}</span><small>{Math.ceil(document.size / 1024)} KB</small>{!readonly && <div className="row"><Button type="button" disabled={state.busy || state.pending} onClick={() => void remove(document, false)}>{t('移除')}</Button><Button type="button" disabled={state.busy || state.pending} className="danger" onClick={() => void remove(document, true)}>{t('删除文件')}</Button></div>}</div>)}
    {!state.loading && !state.pending && !state.documents.length && <p className="muted">{t('暂无关联文件')}</p>}
    {!readonly && <label>{t('上传文件')}<Upload disabled={state.busy || state.pending} showUploadList={false} beforeUpload={file => { void model.upload(file); return false; }}><Button type="button" disabled={state.busy || state.pending}>{t('上传文件')}</Button></Upload></label>}
  </section>;
}
