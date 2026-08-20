import assert from 'node:assert/strict';
import test from 'node:test';
import { APIClient } from './api-client.ts';

const jsonResponse = (status: number, data: unknown) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

test('concurrent 401 responses share one refresh and retry with the new token', async () => {
  let refreshes = 0;
  const authorization: string[] = [];
  const fetcher = async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    if (path.endsWith('/auth/refresh')) {
      refreshes++;
      await new Promise((resolve) => setTimeout(resolve, 10));
      return jsonResponse(200, { code: 200, msg: 'ok', data: { access_token: 'new-token' } });
    }
    authorization.push(new Headers(init?.headers).get('Authorization') || '');
    if (authorization.at(-1) !== 'Bearer new-token') {
      return jsonResponse(401, { code: 401, msg: 'unauthorized', data: null });
    }
    return jsonResponse(200, { code: 200, msg: 'ok', data: {} });
  };
  const client = new APIClient({ fetcher });
  client.setAccessToken('expired-token');

  const responses = await Promise.all([
    client.authorizedFetch('/users'),
    client.authorizedFetch('/devices'),
  ]);

  assert.equal(refreshes, 1);
  assert.deepEqual(
    responses.map((response) => response.status),
    [200, 200],
  );
  assert.equal(authorization.filter((value) => value === 'Bearer new-token').length, 2);
});

test('failed refresh clears the session and notifies once', async () => {
  let notifications = 0;
  const client = new APIClient({
    fetcher: async () => jsonResponse(401, { code: 401, msg: 'unauthorized', data: null }),
    onUnauthorized: () => notifications++,
  });
  client.setAccessToken('expired-token');

  const response = await client.authorizedFetch('/users');

  assert.equal(response.status, 401);
  assert.equal(notifications, 1);
});
