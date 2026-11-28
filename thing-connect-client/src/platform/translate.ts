import { useSyncExternalStore } from 'react';
import messages from './console-en.json';
window.WebI18n?.addMessages(messages);
export const tr = (copy: string): string => window.WebI18n?.t(copy) ?? copy;

const subscribe = (notify: () => void) => window.WebI18n?.onChange(notify) ?? (() => {});
const language = () => window.WebI18n?.getLanguage() || 'zh-CN';
export function usePageLanguage() { useSyncExternalStore(subscribe, language); }
