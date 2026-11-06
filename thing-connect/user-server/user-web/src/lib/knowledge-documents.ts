export interface KnowledgeDocument { document_id: string; name: string; size: number }
interface RequestOptions { method?: string; body?: unknown; form?: FormData; signal?: AbortSignal }
type Transport = <T>(path: string, options?: RequestOptions) => Promise<T>;
interface Pending { uploaded?: KnowledgeDocument; contains?: string; excludes?: string; associate: boolean; desired?: string[] }
export interface DocumentState {
  documents: KnowledgeDocument[]; loading: boolean; busy: boolean; pending: boolean;
  retryAssociation: boolean; notice: string; error?: unknown; errorLabel?: string;
}
const delay = (ms: number, signal: AbortSignal) => new Promise<void>((resolve, reject) => {
  if (signal.aborted) { reject(signal.reason); return; }
  const abort = () => { clearTimeout(timer); reject(signal.reason); };
  const timer = setTimeout(() => { signal.removeEventListener('abort', abort); resolve(); }, ms);
  signal.addEventListener('abort', abort, { once: true });
});

// Owns one index's upload/association/visibility lifecycle. No write is retried
// automatically. Confirmation is bounded, and manual refresh never reuploads.
export class KnowledgeDocuments {
  private state: DocumentState = { documents: [], loading: true, busy: false, pending: false, retryAssociation: false, notice: '' };
  private listeners = new Set<() => void>();
  private lifetime = new AbortController();
  private pending?: Pending;
  private added = new Set<string>();
  private removed = new Set<string>();
  private path: string;
  private transport: Transport;
  private pause: typeof delay;
  constructor(index: string, transport: Transport, pause = delay) { this.transport = transport; this.pause = pause; this.path = '/v1/ai/knowledge/indexes/' + encodeURIComponent(index); }
  snapshot = () => this.state;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  dispose() { this.lifetime.abort(); this.listeners.clear(); }
  private publish(patch: Partial<DocumentState>) {
    if (this.lifetime.signal.aborted) return;
    this.state = { ...this.state, ...patch, pending: !!this.pending, retryAssociation: !!this.pending?.associate };
    this.listeners.forEach(listener => listener());
  }
  private request<T>(path: string, options: RequestOptions = {}) { this.lifetime.signal.throwIfAborted(); return this.transport<T>(path, { ...options, signal: this.lifetime.signal }); }
  private async list() {
    const documents: KnowledgeDocument[] = [];
    for (let page = 1; page <= 1000; page++) {
      const data = await this.request<{ items: KnowledgeDocument[]; total: number }>(this.path + '/documents?page=' + page + '&page_size=100');
      documents.push(...data.items);
      if (documents.length >= data.total) return documents;
      if (!data.items.length) break;
    }
    throw new Error('Incomplete knowledge document pagination');
  }
  private async run(action: () => Promise<void>, errorLabel = '') {
    if (this.state.busy || this.lifetime.signal.aborted) return;
    this.publish({ busy: true, error: undefined, errorLabel: undefined });
    try { await action(); }
    catch (error) { this.publish({ error, errorLabel, notice: this.pending?.associate ? '文件已上传，关联失败，请重试关联' : this.pending ? '操作已提交，文件列表尚未确认，请刷新查看' : '' }); }
    finally { this.publish({ busy: false, loading: false }); }
  }
  private async confirm(repeat: boolean) {
    for (const ms of repeat ? [0, 1000, 2000, 4000, 8000] : [0]) {
      if (ms) await this.pause(ms, this.lifetime.signal);
      const documents = await this.list();
      this.publish({ documents, loading: false });
      const p = this.pending;
      const matches = !p || (!p.associate && (!p.contains || documents.some(d => d.document_id === p.contains)) && (!p.excludes || !documents.some(d => d.document_id === p.excludes)));
      if (matches) {
        if (p?.contains) { this.added.add(p.contains); this.removed.delete(p.contains); }
        if (p?.excludes) { this.removed.add(p.excludes); this.added.delete(p.excludes); }
        this.pending = undefined;
        this.publish({ notice: p ? '文件列表已更新' : '' });
        return;
      }
    }
    this.publish({ notice: '操作已提交，云端尚未同步，请稍后刷新' });
  }
  refresh = () => this.run(async () => {
    // An upload whose association failed must be retried explicitly, rather
    // than being mistaken for a successful operation just because the list loads.
    if (this.pending?.associate) { this.publish({ documents: await this.list() }); return; }
    await this.confirm(false);
  }, '读取知识库文档失败，请重试');
  private async desiredIDs() {
    const detail = await this.request<{ document_ids?: string[] }>(this.path);
    // Preserve this instance's already-confirmed writes if the cloud detail
    // endpoint temporarily lags behind its documents endpoint.
    return [...new Set([...(detail.document_ids || []), ...this.added])].filter(id => !this.removed.has(id));
  }
  private async associate() {
    const p = this.pending;
    if (!p?.uploaded) return;
    p.desired ??= [...new Set([...await this.desiredIDs(), p.uploaded.document_id])];
    await this.request(this.path, { method: 'PUT', body: { document_ids: p.desired } });
    p.associate = false;
    this.publish({ notice: '文件已上传，正在确认关联…' });
    await this.confirm(true);
  }
  upload = (file: File) => {
    if (this.pending) return Promise.resolve();
    return this.run(async () => {
      this.publish({ notice: '正在上传文件…' });
      const form = new FormData(); form.append('file', file);
      const result = await this.request<{ file_id: string }>('/v1/ai/knowledge/files', { method: 'POST', form });
      if (!result.file_id) throw new Error('Upload returned no file ID');
      this.pending = { uploaded: { document_id: result.file_id, name: file.name, size: file.size }, contains: result.file_id, associate: true };
      await this.associate();
    });
  };
  retryAssociation = () => this.run(() => this.associate());
  discardUpload = () => this.run(async () => {
    const id = this.pending?.uploaded?.document_id;
    if (!id || !this.pending?.associate) return;
    await this.request('/v1/ai/knowledge/files/' + encodeURIComponent(id), { method: 'DELETE' });
    this.pending = { excludes: id, associate: false };
    this.publish({ notice: '正在确认文件已删除…' });
    await this.confirm(true);
  });
  remove = (document: KnowledgeDocument, destroy: boolean) => {
    if (this.pending) return Promise.resolve();
    return this.run(async () => {
      if (destroy) await this.request('/v1/ai/knowledge/files/' + encodeURIComponent(document.document_id), { method: 'DELETE' });
      else await this.request(this.path, { method: 'PUT', body: { document_ids: (await this.desiredIDs()).filter(id => id !== document.document_id) } });
      this.pending = { excludes: document.document_id, associate: false };
      this.publish({ notice: '正在确认文件列表…' });
      await this.confirm(true);
    });
  };
}
