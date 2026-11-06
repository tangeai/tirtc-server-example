import { useEffect, useRef, useState, type ReactNode } from 'react';
import { clearSession, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';
export function Dropdown({ label, children, icon }: { label: string; children: ReactNode; icon?: ReactNode }) {
  const ref = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    const close = (event: PointerEvent) => { if (event.target instanceof Node && !ref.current?.contains(event.target) && ref.current) ref.current.open = false; };
    document.addEventListener('pointerdown', close); return () => document.removeEventListener('pointerdown', close);
  }, []);
  return <details ref={ref} className="dropdown" onKeyDown={event => { if (event.key === 'Escape') { event.currentTarget.open = false; event.currentTarget.querySelector('summary')?.focus(); } }}><summary aria-label={label}>{icon}<span className={icon ? 'menu-label' : undefined}>{label}</span>{icon && <span className="menu-chevron" aria-hidden="true">⌄</span>}</summary><div className="dropdown-panel" onClick={event => { if ((event.target as HTMLElement).closest('button,a') && ref.current) ref.current.open = false; }}>{children}</div></details>;
}
export function Account() {
  const { t } = useLanguage();
  const [email, setEmail] = useState(''), [error, setError] = useState(false), [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController(); setError(false);
    void request<{ email: string }>('/v1/user/me', { signal: controller.signal }).then(data => setEmail(data.email)).catch(() => { if (!controller.signal.aborted) setError(true); });
    return () => controller.abort();
  }, [retry]);
  return <Dropdown label={t('账号菜单')} icon={<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><circle cx="12" cy="8" r="4"/><path d="M4 21v-2a8 8 0 0 1 16 0v2"/></svg>}><p className="muted">{t('当前账号')}</p><p>{email || t(error ? '账号信息加载失败' : '正在加载账号…')}</p>{error && <button onClick={() => setRetry(value => value + 1)}>{t('重试')}</button>}<a href="/forgot-password">{t('修改密码')}</a><button className="danger" onClick={() => { clearSession(); location.replace('/login'); }}>{t('退出登录')}</button></Dropdown>;
}
export function NavigationLinks() {
  const { t } = useLanguage();
  const [links, setLinks] = useState<{ name: string; url: string }[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    void request<{ links: { name: string; url: string; enabled: boolean }[] }>('/v1/config/navigation', { anonymous: true, signal: controller.signal }).then(data => {
      setLinks((data.links || []).filter(link => { try { const url = new URL(link.url); return link.enabled && link.name?.trim() && ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password; } catch { return false; } }).slice(0, 3));
    }).catch(() => {});
    return () => controller.abort();
  }, []);
  return links.length ? <Dropdown label={t('相关链接')} icon={<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><path d="m10 13 4-4m-6 7-1 1a4 4 0 0 1-6-6l4-4a4 4 0 0 1 6 0m2 0 1-1a4 4 0 0 1 6 6l-4 4a4 4 0 0 1-6 0"/></svg>}>{links.map(link => <a key={link.url} href={link.url} target="_blank" rel="noopener noreferrer">{link.name} ↗</a>)}</Dropdown> : null;
}
