import { Checkbox, ConfigProvider } from 'antd';
import { Button } from './Button';
import { useEffect, useRef, useState } from 'react';
import { Dialog } from './Dialog';
import { ApiError, errorText, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';
interface Scheme { scheme_id: string; scheme_version: number; name: string; description?: string; output_mode: string; limits: { max_audio_seconds: number } }
interface Config { policy: { enabled: boolean; revision: number; monthly_limit: number }; schemes: Scheme[]; usage: { used: number } }
export function Recognition({ device, close, saved }: { device: string; close(): void; saved(enabled: boolean, count: number): void }) {
  const { t } = useLanguage();
  const [config, setConfig] = useState<Config>(), [catalog, setCatalog] = useState<Scheme[]>([]), [selected, setSelected] = useState<Scheme[]>([]);
  const [enabled, setEnabled] = useState(false), [picker, setPicker] = useState(false), [chosen, setChosen] = useState<string[]>([]);
  const [error, setError] = useState(''), [conflict, setConflict] = useState(false), [busy, setBusy] = useState(false), [loading, setLoading] = useState(true);
  const alive = useRef(true), pending = useRef(false), sequence = useRef(0), dragging = useRef('');
  const url = '/v1/recognition/devices/' + encodeURIComponent(device) + '/config';
  async function load() {
    const rev = ++sequence.current; setLoading(true); setError('');
    try {
      const [types, value] = await Promise.all([request<Scheme[]>('/v1/recognition/types'), request<Config>(url)]);
      if (!alive.current || rev !== sequence.current) return;
      setConfig(value); setCatalog(types); setSelected(value.schemes); setEnabled(value.policy.enabled); setChosen([]); setConflict(false); setPicker(false);
    } catch (e) { if (alive.current && rev === sequence.current) setError(errorText(e)); }
    finally { if (alive.current && rev === sequence.current) setLoading(false); }
  }
  useEffect(() => { void load(); return () => { alive.current = false; sequence.current++; }; }, [device]);
  function move(id: string, position: number) {
    if (busy) return;
    setSelected(items => { const result = [...items]; const from = result.findIndex(item => item.scheme_id === id); if (from < 0 || position < 0 || position >= result.length) return items; const [value] = result.splice(from, 1); result.splice(position, 0, value); return result; });
  }
  async function save() {
    if (pending.current || !config) return;
    const schemes = [...selected, ...catalog.filter(item => chosen.includes(item.scheme_id))];
    if (schemes.length > 8) { setError('最多选择8个识别类型'); return; }
    if (enabled && !schemes.length) { setError('请至少选择一个识别类型'); return; }
    pending.current = true; setBusy(true); setError('');
    try {
      const result = await request<Config>(url, { method: 'PUT', body: { enabled, schemes: schemes.map(({ scheme_id, scheme_version }) => ({ scheme_id, scheme_version })), expected_revision: config.policy.revision } });
      saved(result.policy.enabled, schemes.length);
      if (alive.current) close();
    } catch (e) { if (alive.current) { setError(errorText(e)); setConflict(e instanceof ApiError && e.code === 40901); } }
    finally { pending.current = false; if (alive.current) setBusy(false); }
  }
  return <Dialog title="AI识别设置" onClose={close}><p className="muted">{device}</p>{loading && <p role="status">{t('正在加载…')}</p>}{error && <p role="alert" className="error">{t(error)}</p>}{!config && !loading && <Button onClick={() => void load()}>{t('重试')}</Button>}
    {config && <ConfigProvider componentDisabled={busy || loading}><fieldset disabled={busy || loading}><Checkbox className="radio"  checked={enabled} onChange={e => setEnabled(e.target.checked)}>{t('开启AI识别')}</Checkbox><p className="muted">{t('同一段音频依次分析。拖动手柄排序，或使用 ⋯ 菜单。保存后生效。')}</p>
      <div className="scheme-list">{selected.map((item, index) => <article key={item.scheme_id} className="scheme" data-scheme-id={item.scheme_id}>
        <Button type="button" className="drag" aria-label={t('拖动排序 ') + item.name} onPointerDown={event => { if (event.button !== 0 || busy) return; dragging.current = item.scheme_id; event.currentTarget.setPointerCapture(event.pointerId); }} onPointerUp={event => {
          if (!dragging.current) return; const target = document.elementFromPoint(event.clientX, event.clientY)?.closest('[data-scheme-id]')?.getAttribute('data-scheme-id');
          if (target) move(dragging.current, selected.findIndex(value => value.scheme_id === target)); dragging.current = ''; event.currentTarget.releasePointerCapture(event.pointerId);
        }} onPointerCancel={() => { dragging.current = ''; }}>⠿</Button>
        <div><strong>{index + 1}. {item.name}</strong><p className="muted">{t(({ text: '文字', audio: '语音', text_audio: '文字＋语音' } as Record<string, string>)[item.output_mode] || item.output_mode)} · {item.limits.max_audio_seconds}s</p>{item.description && <p className="muted">{item.description}</p>}<div className="row"><Button type="button" disabled={index === 0} onClick={() => move(item.scheme_id, index - 1)}>{t('上移')}</Button><Button type="button" disabled={index === selected.length - 1} onClick={() => move(item.scheme_id, index + 1)}>{t('下移')}</Button><Button type="button" onClick={() => setSelected(items => items.filter(s => s.scheme_id !== item.scheme_id))}>{t('移除')}</Button></div></div>
      </article>)}</div>
      {picker ? <section className="card">{catalog.filter(item => !selected.some(s => s.scheme_id === item.scheme_id)).map(item => <Checkbox className="radio" key={item.scheme_id}  checked={chosen.includes(item.scheme_id)} onChange={e => setChosen(values => e.target.checked ? [...values, item.scheme_id] : values.filter(v => v !== item.scheme_id))}><span>{item.name}<small>{item.description}</small></span></Checkbox>)}<div className="row"><Button type="button" onClick={() => { if (selected.length + chosen.length > 8) { setError('最多选择8个识别类型'); return; } setSelected(items => [...items, ...catalog.filter(item => chosen.includes(item.scheme_id))]); setChosen([]); setPicker(false); }}>{t('添加所选')}</Button><Button type="button" onClick={() => { setChosen([]); setPicker(false); }}>{t('取消')}</Button></div></section> : <Button type="button" onClick={() => setPicker(true)}>{t('＋ 添加音频类型')}</Button>}
      <p className="muted">{t('本月已占用 {p0} / {p1} 次，每个类型计一次。', { p0: config.usage.used, p1: config.policy.monthly_limit })}</p>
      {conflict && <div role="alert"><p>{t('设置已被其他页面修改，当前草稿已保留。')}</p><Button type="button" onClick={() => setConflict(false)}>{t('继续编辑')}</Button><Button type="button" onClick={() => void load()}>{t('放弃草稿并重新加载')}</Button></div>}
      <div className="row"><Button type="button" onClick={close}>{t('取消')}</Button><Button type="button" className="primary" onClick={() => void save()}>{t(busy ? '保存中…' : '保存设置')}</Button></div>
    </fieldset></ConfigProvider>}
  </Dialog>;
}
