import './console.css';
import { useEffect, useState } from 'react';
import { request, token } from '../services/api';
import { t } from '../../../thing-connect/user-server/user-web/src/lib/messages';
type Link = { name: string; url: string; enabled: boolean };
export function ConsoleHeader() {
  const [language, setLanguage] = useState(window.WebI18n?.getLanguage());
  const canSwitch = window.WebLanguageConfig?.allow_switch;
  useEffect(() => window.WebI18n?.onChange(() => setLanguage(window.WebI18n?.getLanguage())), []);
  function toggle() { window.WebI18n?.setLanguage(language === 'en' ? 'zh-CN' : 'en'); }
  const [links, setLinks] = useState<Link[]>([]);
  useEffect(() => {
    let alive = true;
    void request<{links: Link[]}>('/v1/config/navigation').then(result => {
      if (alive) setLinks((result?.links || []).filter(link => {
        try {
          const url = new URL(link.url);
          return link.enabled && link.name?.trim() && ['http:', 'https:'].includes(url.protocol) && !url.username && !url.password;
        } catch { return false; }
      }).slice(0, 3));
    }).catch(() => {});
    return () => { alive = false; };
  }, []);
  return <header className='console-nav'><a className='console-brand' href='/devices'><img src='/static/xiaotai-logo-transparent.png' alt='' />{t('小钛控制台')}</a>
    <nav>{token() && <><a href='/devices'>{t('我的设备')}</a><a href='/account'>{t('我的账号')}</a></>}
    {(!!links.length || canSwitch) && <details><summary>{t('更多')}</summary><div>{links.map(link => <a key={link.url} href={link.url} target='_blank' rel='noopener noreferrer'>{link.name} ↗</a>)}
      {canSwitch && <button onClick={toggle}>{language === 'en' ? '中文' : 'English'}</button>}</div></details>}</nav>
  </header>;
}
