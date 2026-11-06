import { useEffect, useRef, useState } from 'react';
import { useLanguage } from '../lib/i18n';

export function LanguageSwitcher() {
  const { language, canSwitch } = useLanguage();
  const [open, setOpen] = useState(false);
  const container = useRef<HTMLDivElement>(null), trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (!container.current?.contains(event.target as Node)) setOpen(false); };
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setOpen(false); trigger.current?.focus(); } };
    document.addEventListener('pointerdown', outside); document.addEventListener('keydown', escape);
    return () => { document.removeEventListener('pointerdown', outside); document.removeEventListener('keydown', escape); };
  }, [open]);
  if (!canSwitch) return null;
  const select = (value: string) => { window.WebI18n?.setLanguage(value); setOpen(false); trigger.current?.focus(); };
  return <div className="language-switcher" ref={container} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}>
    <button ref={trigger} className="language-trigger" type="button" aria-label="语言 / Language" aria-expanded={open} onClick={() => setOpen(value => !value)}>
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18M5 6.5h14M5 17.5h14"/></svg>
      <span className="language-label">{language === 'en' ? 'English' : '简体中文'}</span><span className="language-chevron" aria-hidden="true">⌄</span>
    </button>
    {open && <div className="language-panel" role="group" aria-label="语言 / Language">{[['zh-CN', '简体中文'], ['en', 'English']].map(([value, label]) => <button key={value} type="button" lang={value} aria-pressed={language === value} onClick={() => select(value)}><span>{label}</span><span aria-hidden="true">{language === value ? '✓' : ''}</span></button>)}</div>}
  </div>;
}
