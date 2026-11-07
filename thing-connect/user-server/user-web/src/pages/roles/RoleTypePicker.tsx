import { useEffect, useRef, useState } from 'react';
import { useLanguage } from '../../lib/i18n';
import type { Voice } from '../../lib/roles';
import type { RoleType } from '../../lib/role-types';

export function RoleTypePicker({ items, voices, selected, currentVoiceName, disabled, onOpen, onSelect }: { items: RoleType[]; voices: Voice[]; selected?: RoleType; currentVoiceName?: string; disabled: boolean; onOpen?(): void; onSelect(item: RoleType): Promise<void> }) {
  const { t } = useLanguage();
  const [open, setOpen] = useState(false), [query, setQuery] = useState(''), [playing, setPlaying] = useState(''), [error, setError] = useState('');
  const container = useRef<HTMLDivElement>(null), trigger = useRef<HTMLButtonElement>(null), audio = useRef<HTMLAudioElement>(null), intent = useRef(0);
  const stop = () => { intent.current++; audio.current?.pause(); setPlaying(''); };
  useEffect(() => { const outside = (event: PointerEvent) => { if (!container.current?.contains(event.target as Node)) { setOpen(false); stop(); } }; document.addEventListener('pointerdown', outside); return () => { document.removeEventListener('pointerdown', outside); intent.current++; audio.current?.pause(); }; }, []);
  useEffect(() => { if (disabled) { stop(); setOpen(false); } }, [disabled]);
  const filtered = items.filter(item => { const voice = voices.find(v => v.id === item.voice_id); return [item.name, voice?.name || ''].some(value => value.toLowerCase().includes(query.toLowerCase())); });
  async function preview(item: RoleType) {
    const voice = voices.find(v => v.id === item.voice_id), player = audio.current;
    if (!player || !voice?.sample_url) return;
    if (playing === item.id) { stop(); return; }
    stop(); const current = intent.current; setError(''); player.src = voice.sample_url;
    try { await player.play(); if (current === intent.current) setPlaying(item.id); }
    catch { if (current === intent.current) { setPlaying(''); setError(t('试听失败，请重试')); } }
  }
  return <div className="role-type-picker" ref={container} onKeyDown={event => { if (event.key === 'Escape') { event.stopPropagation(); setOpen(false); stop(); trigger.current?.focus(); } }}>
    <label id="role-type-label">{t('角色类型')}</label><div className="role-type-controls"><button ref={trigger} type="button" aria-labelledby="role-type-label role-type-selected" aria-expanded={open} disabled={disabled} onClick={() => { setOpen(!open); if (open) stop(); else onOpen?.(); }}><span id="role-type-selected">{selected?.name || (currentVoiceName ? t('自定义角色 · {p0}', { p0: currentVoiceName }) : t('选择角色类型'))}</span><span aria-hidden="true">⌄</span></button></div>
    {open && <div className="role-type-menu"><input autoFocus type="search" aria-label={t('搜索角色类型')} placeholder={t('搜索角色类型')} value={query} onChange={e => setQuery(e.target.value)}/><div className="role-type-options">{filtered.map(item => { const voice = voices.find(v => v.id === item.voice_id); return <div className="role-type-option" key={item.id}><button type="button" disabled={disabled} aria-pressed={selected?.id === item.id} onClick={async () => { stop(); await onSelect(item); setOpen(false); trigger.current?.focus(); }}><span>{selected?.id === item.id ? '✓ ' : ''}{item.name}</span><small>{voice?.name}</small></button>{voice?.sample_url ? <button className="role-type-preview" type="button" disabled={disabled} aria-label={t(playing === item.id ? '停止试听：{p0}' : '试听：{p0}', { p0: item.name })} onClick={() => void preview(item)}>{playing === item.id ? '□' : '▷'}</button> : <span className="role-type-no-preview">{t('暂无试听')}</span>}</div>; })}{!filtered.length && <p className="muted">{t('没有匹配的角色类型')}</p>}</div></div>}
    <audio ref={audio} preload="none" onEnded={() => setPlaying('')} onError={() => { setPlaying(''); setError(t('试听失败，请重试')); }}/>{error && <p role="alert" className="error">{error}</p>}
  </div>;
}
