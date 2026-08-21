import assert from 'node:assert/strict';
import test from 'node:test';
import { isValidAdminPassword } from './password-policy.ts';

test('管理员密码至少八位并包含大小写字母和数字', () => {
  assert.equal(isValidAdminPassword('Abcdefg1'), true);
  assert.equal(isValidAdminPassword('中文Abcde1'), true);
  assert.equal(isValidAdminPassword('Abcdef1'), false);
  assert.equal(isValidAdminPassword('abcdefg1'), false);
  assert.equal(isValidAdminPassword('ABCDEFG1'), false);
  assert.equal(isValidAdminPassword('Abcdefgh'), false);
});
