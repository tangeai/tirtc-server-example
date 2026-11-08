import { Popover, Button as AntButton } from 'antd';
import { Button } from './Button';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { clearSession, request } from '../lib/api';
import { useLanguage } from '../lib/i18n';
export function Dropdown({ label, children, icon }: { label: string; children: ReactNode; icon?: ReactNode }) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  return <Popover destroyOnHidden trigger="click" placement="bottomRight" open={open} onOpenChange={setOpen} content={<div className="account-menu" onKeyDown={event => { if (event.key === 'Escape') { event.stopPropagation(); setOpen(false); trigger.current?.focus(); } }} onClick={event => { if ((event.target as HTMLElement).closest('button,a')) setOpen(false); }}>{children}</div>}>
    <AntButton ref={trigger} htmlType="button" type={icon ? 'text' : 'default'} className="menu-trigger" aria-label={label} aria-expanded={open} onKeyDown={event=>{if(event.key==='Escape'){setOpen(false);}}}>{icon}<span className={icon ? 'menu-label' : undefined}>{label}</span>{icon && <span className="menu-chevron" aria-hidden="true">⌄</span>}</AntButton>
  </Popover>;
}
export function Account() {
  const { t } = useLanguage();
  const [email, setEmail] = useState(''), [error, setError] = useState(false), [retry, setRetry] = useState(0);
  useEffect(() => {
    const controller = new AbortController(); setError(false);
    void request<{ email: string }>('/v1/user/me', { signal: controller.signal }).then(data => setEmail(data.email)).catch(() => { if (!controller.signal.aborted) setError(true); });
    return () => controller.abort();
  }, [retry]);
  return <Dropdown label={t('账号菜单')} icon={<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" aria-hidden="true"><circle cx="12" cy="8" r="4"/><path d="M4 21v-2a8 8 0 0 1 16 0v2"/></svg>}><p className="muted">{t('当前账号')}</p><p>{email || t(error ? '账号信息加载失败' : '正在加载账号…')}</p>{error && <Button onClick={() => setRetry(value => value + 1)}>{t('重试')}</Button>}<a href="/forgot-password">{t('修改密码')}</a><Button className="danger" onClick={() => { clearSession(); location.replace('/login'); }}>{t('退出登录')}</Button></Dropdown>;
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
