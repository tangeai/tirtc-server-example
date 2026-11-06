import { test } from 'node:test';
import assert from 'node:assert/strict';
import { KnowledgeDocuments } from './knowledge-documents.ts';

function fixture() {
  let uploads = 0, writes = 0, reads = 0, visible = false, failLink = false;
  let file = '', desired: string[] = [], delays: number[] = [];
  const model = new KnowledgeDocuments('index', async <T>(path: string, options: { method?: string; body?: unknown } = {}) => {
    let data: unknown = {};
    if (options.method === 'POST') { uploads++; file = 'file-' + uploads; data = { file_id: file }; }
    else if (options.method === 'PUT') { writes++; if (failLink) { failLink = false; throw new Error('link failed'); } desired = (options.body as { document_ids: string[] }).document_ids; }
    else if (path.includes('/documents')) { reads++; const items = visible && desired.includes(file) ? [{ document_id: file, name: 'test.txt', size: 1 }] : []; data = { items, total: items.length }; }
    else data = { document_ids: [] }; // A stale detail endpoint.
    return data as T;
  }, async ms => { delays.push(ms); });
  return { model, file: () => file, counts: () => ({ uploads, writes, reads }), desired: () => desired, delays: () => delays, visible: (value: boolean) => { visible = value; }, fail: () => { failLink = true; } };
}
const file = () => new File(['test'], 'test.txt', { type: 'text/plain' });
test('confirmation is bounded; refresh confirms without a second upload or write', async () => {
  const f = fixture(); await f.model.refresh(); await f.model.upload(file());
  assert.equal(f.model.snapshot().pending, true); assert.equal(f.model.snapshot().busy, false);
  assert.equal(f.model.snapshot().notice, '操作已提交，云端尚未同步，请稍后刷新');
  assert.deepEqual(f.delays(), [1000, 2000, 4000, 8000]);
  await f.model.upload(file()); assert.equal(f.counts().uploads, 1);
  f.visible(true); await f.model.refresh();
  assert.equal(f.model.snapshot().pending, false); assert.equal(f.model.snapshot().documents.length, 1);
  assert.equal(f.counts().uploads, 1); assert.equal(f.counts().writes, 1);
});
test('association failure retains the uploaded ID, retries only the link, and preserves confirmed earlier files', async () => {
  const f = fixture(); f.visible(true); await f.model.upload(file()); const first = f.file();
  f.fail(); await f.model.upload(file());
  assert(f.model.snapshot().retryAssociation); assert(f.model.snapshot().error);
  await f.model.refresh(); assert(f.model.snapshot().retryAssociation, 'refresh must not discard the unassociated upload');
  await f.model.retryAssociation();
  assert.equal(f.counts().uploads, 2); assert.equal(f.model.snapshot().pending, false);
  assert(f.desired().includes(first), 'stale detail must not drop this instance\'s confirmed upload');
});
test('duplicate upload while the first request is pending is ignored', async () => {
  let release!: (value: { file_id: string }) => void, uploads = 0;
  const blocked = new Promise<{ file_id: string }>(r => { release = r; });
  const model = new KnowledgeDocuments('index', async <T>(_p: string, o: { method?: string } = {}) => {
    if (o.method === 'POST') { uploads++; return await blocked as T; }
    return { items: [{ document_id: 'one', name: 'test', size: 1 }], total: 1 } as T;
  });
  const pending = model.upload(file()); await model.upload(file()); assert.equal(uploads, 1);
  release({ file_id: 'one' }); await pending; assert.equal(model.snapshot().pending, false);
});
test('disposing an index cancels late follow-up writes and notifications', async () => {
  let release!: (value: { file_id: string }) => void, requests = 0, notifications = 0;
  const blocked = new Promise<{ file_id: string }>(r => { release = r; });
  const model = new KnowledgeDocuments('old', async <T>() => { requests++; return await blocked as T; });
  model.subscribe(() => { notifications++; });
  const work = model.upload(file()); model.dispose(); const before = notifications;
  release({ file_id: 'old-file' }); await work;
  assert.equal(requests, 1); assert.equal(notifications, before);
});
test('failed association can be explicitly cleaned up without reupload', async () => {
  const f = fixture(); f.fail(); await f.model.upload(file()); assert(f.model.snapshot().retryAssociation);
  await f.model.discardUpload(); assert.equal(f.model.snapshot().pending, false); assert.equal(f.counts().uploads, 1);
});
