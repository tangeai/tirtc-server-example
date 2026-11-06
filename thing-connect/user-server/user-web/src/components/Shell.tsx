import { LanguageSwitcher } from './LanguageSwitcher';
import type { ReactNode } from 'react';
import { useLanguage } from '../lib/i18n';

export function Shell({ title, back = '/devices', backLabel = '返回设备列表', children }: {
  title: string; back?: string | null; backLabel?: string; children: ReactNode;
}) {
  const { t } = useLanguage();
  return <><header className="topbar"><div className="heading">{back && <><a className="back" href={back} onClick={event => { if (window.MiniProgramPage?.embedded) { event.preventDefault(); window.MiniProgramPage.goBack(); } }}>← {t(backLabel)}</a><span className="divider"/></>}<strong>{t(title)}</strong></div>
    <div className="header-actions"><LanguageSwitcher/></div></header>{children}</>;
}
