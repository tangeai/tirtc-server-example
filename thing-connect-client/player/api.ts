declare global {
  interface Window {
    MiniProgramPage: { embedded: boolean; token: string | null; goBack(): void; requireLogin(): void };
    WebLanguageConfig?: { allow_switch: boolean };
    WebI18n?: {
      t(key: string): string;
      getLanguage(): string;
      setLanguage(language: string): void;
      onChange(listener: () => void): () => void;
      addMessages(messages: Record<string, string>): void;
    };
  }
}

export function sessionToken(): string | null {
  if (window.MiniProgramPage.embedded) return window.MiniProgramPage.token;
  const raw = localStorage.getItem('token');
  if (!raw || !raw.trimStart().startsWith('{')) return raw;
  try {
    const value = JSON.parse(raw).data;
    if (typeof value === 'string' && value.length > 0) {
      localStorage.setItem('token', value);
      return value;
    }
  } catch { /* Invalid legacy wrapper requires a new browser session. */ }
  localStorage.removeItem('token');
  return null;
}

export async function get<T>(path: string, signal: AbortSignal): Promise<T> {
  const token = sessionToken();
  if (!token) {
    window.MiniProgramPage.requireLogin();
    throw new Error('请重新登录');
  }
  const response = await fetch(path, { headers: { Authorization: 'Bearer ' + token }, cache: 'no-store', signal });
  const result = response.status === 401 ? { code: 401 } : await response.json();
  if (signal.aborted || sessionToken() !== token) throw new Error('会话已结束');
  if (result.code === 401) {
    if (!window.MiniProgramPage.embedded) localStorage.removeItem('token');
    window.MiniProgramPage.requireLogin();
    throw new Error('请重新登录');
  }
  if (!response.ok || result.code !== 200) throw new Error('请求失败');
  return result.data;
}
