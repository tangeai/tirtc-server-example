import test from 'node:test';
import assert from 'node:assert/strict';
import { errorText } from './api.ts';

test('network and timeout errors use the site language', () => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'window');
  try {
    Object.defineProperty(globalThis, 'window', {configurable: true, value: {WebI18n: {t: (key: string) => ({
      '请求超时，请检查网络后重试': 'Request timed out. Check your connection and try again.',
      '网络连接失败，请稍后重试': 'Network connection failed. Please try again later.',
    })[key]}}});
    assert.match(errorText(new DOMException('timeout', 'TimeoutError')), /^Request timed out/);
    assert.match(errorText(new DOMException('abort', 'AbortError')), /^Request timed out/);
    assert.match(errorText(new TypeError('Failed to fetch')), /^Network connection failed/);
  } finally {
    if(previous) Object.defineProperty(globalThis,'window',previous);
    else Reflect.deleteProperty(globalThis,'window');
  }
});
