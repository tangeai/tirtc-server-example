import { ConfigProvider } from 'antd';
import enUS from 'antd/locale/en_US';
import zhCN from 'antd/locale/zh_CN';
import type { ReactNode } from 'react';
import { useLanguage } from '../lib/i18n';

export function UIProvider({ children }: { children: ReactNode }) {
  const { language } = useLanguage();
  return <ConfigProvider virtual={false} locale={language === 'en' ? enUS : zhCN} button={{ autoInsertSpace: false }}
    theme={{ token: { colorPrimary: '#07866f', borderRadius: 8, controlHeight: 36, fontFamily: 'inherit' }, components: { Collapse: { headerPadding: '8px 12px', contentPadding: '8px 12px' }, Modal: { titleFontSize: 18, titleLineHeight: 1.4, titleColor: '#172026' } } }}>{children}</ConfigProvider>;
}
