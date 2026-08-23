import { APIClient } from './api-client';
import { executeAdminDownload, executeAdminRequest } from './api-response';
import { reportError } from './error-feedback';

const client = new APIClient({
  fetcher: globalThis.fetch.bind(globalThis),
  onUnauthorized: () => window.dispatchEvent(new Event('admin:unauthorized')),
  onRequestError: reportError,
});

export const setAccessToken = (value: string) => client.setAccessToken(value);
export const restoreSession = () => client.restoreSession();

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  return executeAdminRequest<T>(path, init, client.authorizedFetch.bind(client), reportError);
}
export async function download(path: string, filename: string) {
  const blob = await executeAdminDownload(path, client.authorizedFetch.bind(client), reportError);
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
export const json = (method: string, value: unknown): RequestInit => ({
  method,
  body: JSON.stringify(value),
});
