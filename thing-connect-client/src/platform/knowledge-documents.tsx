import { tr } from './translate';
import { useEffect, useMemo, useSyncExternalStore } from 'react';
import { KnowledgeDocuments } from '../../../thing-connect/user-server/user-web/src/lib/knowledge-documents';
import { request, errorText } from '../../../thing-connect/user-server/user-web/src/lib/api';

export function Documents({ index, readonly }: { index: string; readonly: boolean }) {
  const model = useMemo(() => new KnowledgeDocuments(index, request), [index]);
  const state = useSyncExternalStore(model.subscribe, model.snapshot);
  useEffect(() => { void model.refresh(); return () => model.dispose(); }, [model]);
  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (state.busy || state.pending) { event.preventDefault(); event.returnValue = ''; }
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [state.busy, state.pending]);
  const locked = state.busy || state.pending;
  return <section className='knowledge-files'>
    <h3>{tr("关联文件")}</h3>
    <button disabled={state.busy} onClick={() => void model.refresh()}>{tr("刷新文件列表")}</button>
    {state.documents.map(file => <div key={file.document_id}>{file.name} · {Math.ceil(file.size / 1024)} KB
      {!readonly && <><button disabled={locked} onClick={() => { if (confirm('从此知识库移除文件？')) void model.remove(file, false); }}>{tr("移除关联")}</button>
      <button disabled={locked} onClick={() => { if (confirm('永久删除此文件？')) void model.remove(file, true); }}>{tr("删除文件")}</button></>}
    </div>)}
    {!readonly && <label>{tr("上传文件")}<input type='file' disabled={locked} onChange={event => {
      const file = event.currentTarget.files?.[0]; event.currentTarget.value = '';
      if (file) void model.upload(file);
    }}/></label>}
    {state.retryAssociation && <><button disabled={state.busy} onClick={() => void model.retryAssociation()}>{tr("重试关联")}</button>
      <button disabled={state.busy} onClick={() => { if (confirm('删除已上传的文件？')) void model.discardUpload(); }}>{tr("删除已上传文件")}</button></>}
    <p role='status'>{state.notice || (state.loading ? tr("正在读取文件…") : '')}</p>
    {!!state.error && <p role='alert'>{state.errorLabel || errorText(state.error)}</p>}
  </section>;
}
