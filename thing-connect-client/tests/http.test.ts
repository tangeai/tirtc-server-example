import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ApiError, decodeResponse } from '../src/domain/http.ts';
test('不同业务成功码明确区分', () => {
  assert.equal(decodeResponse(200, { code: 200, data: 'user' }), 'user');
  assert.equal(decodeResponse(200, { code: 0, data: 'voip' }, 0), 'voip');
  assert.throws(() => decodeResponse(200, { code: 0 }), ApiError);
});
test('HTTP 200 中的业务错误仍然失败，不比较 msg', () => {
  assert.throws(() => decodeResponse(200, { code: 40300, msg: '无权访问' }), (error: unknown) => error instanceof ApiError && error.code === 40300);
  assert.equal(decodeResponse(200, { code: 200, msg: 'different', data: 42 }), 42);
});
test('网关 HTML、登录错误和 JWT 失效保持不同错误码', () => {
  assert.throws(() => decodeResponse(502, '<html>internal</html>'), (error: unknown) => error instanceof ApiError && !error.message.includes('internal'));
  assert.throws(() => decodeResponse(401, { code: 4091, msg: '邮箱或密码错误' }), (error: unknown) => error instanceof ApiError && error.code === 4091);
  assert.throws(() => decodeResponse(401, ''), (error: unknown) => error instanceof ApiError && error.code === 401);
});
