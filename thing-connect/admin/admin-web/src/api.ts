import { APIClient, type Envelope } from './api-client';

const client = new APIClient({
  fetcher: globalThis.fetch.bind(globalThis),
  onUnauthorized: () => window.dispatchEvent(new Event('admin:unauthorized')),
});

export const setAccessToken = (value: string) => client.setAccessToken(value);
export const restoreSession = () => client.restoreSession();

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await client.authorizedFetch(path, init);
  let body: Envelope<T>;
  try {
    body = (await response.json()) as Envelope<T>;
  } catch {
    throw new Error(`服务器返回了无效响应（HTTP ${response.status}）`);
  }
  if (!response.ok || body.code !== 200) throw new Error(body.msg || `HTTP ${response.status}`);
  return body.data;
}
export async function download(path: string, filename: string) {
  const response = await client.authorizedFetch(path);
  if (!response.ok) {
    let msg = `HTTP ${response.status}`;
    try {
      msg = ((await response.json()) as Envelope<unknown>).msg || msg;
    } catch {
      // Keep the HTTP status when the server did not return the API envelope.
    }
    throw new Error(msg);
  }
  const url = URL.createObjectURL(await response.blob());
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
