import { useSyncExternalStore } from 'react';
import userMessages from '../../../static/user-i18n.json';
import callMessages from '../../../../call-server/handler/call-i18n.json';
import aiMessages from '../../../../ai-server/static/ai-i18n.json';

declare global {
  interface Window {
    WebLanguageConfig?: { allow_switch: boolean; default: string };
    WebI18n?: {
      t(key: string, params?: Record<string, string | number>): string;
      getLanguage(): string;
      setLanguage(language: string): void;
      onChange(listener: () => void): () => void;
      addMessages(messages: Record<string, string>): void;
      clearLocalSession(): void;
      errorMessage(body: { code: number; msg?: string }, fallback?: string): string;
    };
  }
}
window.WebI18n?.addMessages({ ...userMessages, ...callMessages, ...aiMessages });
const subscribe = (listener: () => void) => window.WebI18n?.onChange(listener) ?? (() => {});
const snapshot = () => window.WebI18n?.getLanguage() ?? 'zh-CN';
export const t = (key: string, params?: Record<string, string | number>) => window.WebI18n?.t(key, params) ?? key;
export function useLanguage() {
  const language = useSyncExternalStore(subscribe, snapshot);
  return { t, language, canSwitch: !!window.WebLanguageConfig?.allow_switch,
    toggle: () => window.WebI18n?.setLanguage(language === 'en' ? 'zh-CN' : 'en') };
}
