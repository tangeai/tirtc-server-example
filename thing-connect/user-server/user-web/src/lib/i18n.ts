import { useSyncExternalStore } from 'react';
import { t } from './messages';
export { t };
const subscribe = (listener: () => void) => window.WebI18n?.onChange(listener) ?? (() => {});
const snapshot = () => window.WebI18n?.getLanguage() ?? 'zh-CN';
export function useLanguage() {
  const language = useSyncExternalStore(subscribe, snapshot);
  return { t, language, canSwitch: !!window.WebLanguageConfig?.allow_switch,
    toggle: () => window.WebI18n?.setLanguage(language === 'en' ? 'zh-CN' : 'en') };
}
