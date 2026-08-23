export type Envelope<T> = { code: number; msg: string; data: T };

type Fetcher = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

type ClientOptions = {
  fetcher: Fetcher;
  basePath?: string;
  onUnauthorized?: () => void;
  onRequestError?: (error: unknown) => void;
};

export class APIClient {
  private accessToken = '';
  private refreshPromise?: Promise<boolean>;
  private readonly fetcher: Fetcher;
  private readonly basePath: string;
  private readonly onUnauthorized?: () => void;
  private readonly onRequestError?: (error: unknown) => void;

  constructor({ fetcher, basePath = '/v1/admin', onUnauthorized, onRequestError }: ClientOptions) {
    this.fetcher = fetcher;
    this.basePath = basePath;
    this.onUnauthorized = onUnauthorized;
    this.onRequestError = onRequestError;
  }

  setAccessToken(value: string) {
    this.accessToken = value;
  }

  async restoreSession() {
    return this.refreshAccessToken(false);
  }

  async authorizedFetch(path: string, init: RequestInit = {}) {
    const tokenUsed = this.accessToken;
    let response = await this.request(path, init);
    if (response.status !== 401 || path === '/auth/refresh' || path === '/auth/login') {
      return response;
    }

    // Another request may already have completed a refresh after this request
    // was sent. Retry with that newer access token before rotating again.
    if (this.accessToken && this.accessToken !== tokenUsed) {
      response = await this.request(path, init);
      if (response.status !== 401) return response;
    }

    const refreshed = await this.refreshAccessToken(true);
    if (!refreshed) return response;
    response = await this.request(path, init);
    if (response.status === 401) this.expireSession();
    return response;
  }

  private request(path: string, init: RequestInit) {
    const headers = new Headers(init.headers);
    if (init.body && !headers.has('Content-Type') && !(init.body instanceof FormData)) {
      headers.set('Content-Type', 'application/json');
    }
    headers.set('X-Admin-Request', '1');
    if (this.accessToken) headers.set('Authorization', `Bearer ${this.accessToken}`);
    return this.fetcher(`${this.basePath}${path}`, {
      ...init,
      headers,
      credentials: 'same-origin',
    });
  }

  private refreshAccessToken(notify: boolean): Promise<boolean> {
    if (this.refreshPromise) return this.refreshPromise;
    this.refreshPromise = this.fetcher(`${this.basePath}/auth/refresh`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'X-Admin-Request': '1' },
    })
      .then(async (response) => {
        if (!response.ok) return false;
        const body = (await response.json()) as Envelope<{ access_token?: string }>;
        if (body.code !== 200 || !body.data?.access_token) return false;
        this.accessToken = body.data.access_token;
        return true;
      })
      .catch((error: unknown) => {
        this.onRequestError?.(error);
        return false;
      })
      .then((success) => {
        if (!success) {
          this.accessToken = '';
          if (notify) this.onUnauthorized?.();
        }
        return success;
      })
      .finally(() => {
        this.refreshPromise = undefined;
      });
    return this.refreshPromise;
  }

  private expireSession() {
    this.accessToken = '';
    this.onUnauthorized?.();
  }
}
