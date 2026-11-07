import { Button } from './Button';
import { useEffect, useRef, useState } from 'react';
import { Dialog } from './Dialog';
import { useConfirm } from './Confirm';
import { errorText, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';

interface Entry { id: string; device_id: string; assistant_id: string; started_at: string | null; reference: string }
interface Message { role: string; content: string; time: string }
interface Page { items: Entry[]; page: number; page_size: number; has_more: boolean }
export function Conversations({ device, name, close }: { device: string; name: string; close(): void }) {
  const { t, language } = useLanguage(), ask = useConfirm();
  const [page, setPage] = useState<Page>({ items: [], page: 1, page_size: 10, has_more: false });
  const [selected, setSelected] = useState<Entry>(), [messages, setMessages] = useState<Message[]>([]);
  const [loading, setLoading] = useState(true), [reading, setReading] = useState(false), [deleting, setDeleting] = useState(false);
  const [error, setError] = useState(''), [detailError, setDetailError] = useState(''), [notice, setNotice] = useState(''), [showDetail, setShowDetail] = useState(false);
  const live = useRef(true), pending = useRef(false), listRequest = useRef<AbortController | undefined>(undefined), detailRequest = useRef<AbortController | undefined>(undefined);
  const transcript = useRef<HTMLDivElement>(null);
  const base = '/v1/ai/device/' + encodeURIComponent(device) + '/conversations';
  const stamp = (value: string | null, timeOnly = false) => {
    if (!value) return t('时间未知');
    const date = new Date(value); if (Number.isNaN(date.getTime())) return t('时间未知');
    return timeOnly ? date.toLocaleTimeString(language, { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : date.toLocaleString(language, { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
  };
  async function read(entry: Entry) {
    detailRequest.current?.abort(); const controller = new AbortController(); detailRequest.current = controller;
    setSelected(entry); setMessages([]); setDetailError(''); setReading(true);
    try {
      const result = await request<{ items: Message[] }>(base + '/' + encodeURIComponent(entry.id), { signal: controller.signal, headers: { 'X-Conversation-Reference': entry.reference } });
      if (!live.current || controller.signal.aborted) return;
      setMessages(result.items); if (transcript.current) transcript.current.scrollTop = 0;
    } catch (e) { if (live.current && !controller.signal.aborted) setDetailError(errorText(e)); }
    finally { if (live.current && !controller.signal.aborted) setReading(false); }
  }
  async function load(number: number, preferred?: string) {
    listRequest.current?.abort(); detailRequest.current?.abort(); const controller = new AbortController(); listRequest.current = controller;
    setLoading(true); setError(''); setDetailError(''); setSelected(undefined); setMessages([]); setReading(false);
    try {
      const result = await request<Page>(base + '?page=' + number + '&page_size=10', { signal: controller.signal });
      if (!live.current || controller.signal.aborted) return;
      setPage(result); const entry = result.items.find(item => item.id === preferred) || result.items[0];
      if (entry) void read(entry); else setShowDetail(false);
    } catch (e) { if (live.current && !controller.signal.aborted) setError(errorText(e)); }
    finally { if (live.current && !controller.signal.aborted) setLoading(false); }
  }
  useEffect(() => { void load(1); return () => { live.current = false; listRequest.current?.abort(); detailRequest.current?.abort(); }; }, []);
  async function remove() {
    if (!selected || pending.current) return;
    pending.current = true; setDeleting(true); const entry = selected;
    try {
      if (await ask({ title: '删除会话', message: '删除后无法恢复，确定删除这条会话记录？', choices: [{ value: 'cancel', label: '取消' }, { value: 'delete', label: '确认删除' }] }) !== 'delete' || !live.current) return;
      setDetailError(''); setNotice('');
      await request(base + '/' + encodeURIComponent(entry.id), { method: 'DELETE', headers: { 'X-Conversation-Reference': entry.reference } });
      if (!live.current) return;
      setNotice('会话已删除'); setShowDetail(false);
      await load(page.items.length === 1 && page.page > 1 ? page.page - 1 : page.page);
    } catch (e) { if (live.current) setDetailError(errorText(e)); }
    finally { pending.current = false; if (live.current) setDeleting(false); }
  }
  return <Dialog title={t('{p0} · 会话记录', { p0: name || t('未命名设备') })} size="history" onClose={close} closeDisabled={deleting}>
    <div className={'history-layout' + (showDetail ? ' history-show-detail' : '')}>
      <aside className="history-list" aria-label={t('会话列表')}>
        <div className="history-toolbar"><strong>{t('历史对话')}</strong><Button disabled={loading || deleting} onClick={() => void load(page.page, selected?.id)}>{t('刷新')}</Button></div>
        <div className="history-entries">
          {loading ? <p role="status">{t('正在加载会话…')}</p> : error ? <p role="alert" className="error">{t(error)}</p> : !page.items.length ? <p className="muted">{t('暂无会话记录')}</p> : page.items.map(entry => <Button key={entry.id} className="history-entry" aria-pressed={selected?.id === entry.id} disabled={deleting} onClick={() => { setShowDetail(true); void read(entry); }}>
            <strong>{stamp(entry.started_at)}</strong><span>{t('会话记录')}</span>
          </Button>)}
        </div>
        <nav className="history-pages" aria-label={t('会话分页')}><Button disabled={loading || deleting || page.page <= 1} onClick={() => void load(page.page - 1)}>{t('上一页')}</Button><span>{t('第 {p0} 页', { p0: page.page })}</span><Button disabled={loading || deleting || !page.has_more} onClick={() => void load(page.page + 1)}>{t('下一页')}</Button></nav>
      </aside>
      <section className="history-detail" aria-label={t('会话详情')}>
        <div className="history-toolbar"><Button className="history-back" disabled={deleting} onClick={() => setShowDetail(false)}>← {t('返回列表')}</Button><strong>{selected ? stamp(selected.started_at) : t('会话详情')}</strong>{selected && <Button className="danger" disabled={deleting || loading || reading} onClick={() => void remove()}>{t('删除会话')}</Button>}</div>
        {notice && <p className="success" role="status">{t(notice)}</p>}
        {detailError && <div className="history-error"><p className="error" role="alert">{t(detailError)}</p>{selected && <Button disabled={deleting} onClick={() => void read(selected)}>{t('重试')}</Button>}</div>}
        <div className="history-messages" ref={transcript}>
          {reading ? <p role="status">{t('正在加载对话…')}</p> : !selected ? <p className="history-empty muted">{t(loading ? '正在加载会话…' : '选择左侧会话查看内容')}</p> : !detailError && !messages.length ? <p className="history-empty muted">{t('这条会话暂无文字内容')}</p> : messages.map((message, index) => <article key={index} className={'history-message ' + (message.role === 'user' ? 'history-user' : 'history-assistant')}>
            <div className="history-message-meta"><strong>{t(message.role === 'user' ? '用户' : message.role === 'assistant' ? 'AI 助手' : message.role === 'system' ? '系统' : '工具')}</strong><time dateTime={message.time}>{stamp(message.time, true)}</time></div>
            <div className="history-bubble">{message.content}</div>
          </article>)}
        </div>
      </section>
    </div>
  </Dialog>;
}
