import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs';
import vm from 'node:vm';
import ts from 'typescript';

test('插件宽度采用支持的单位，不能使用百分比', () => {
  const template = fs.readFileSync(new URL('../src/native/captcha/index.wxml', import.meta.url), 'utf8');
  const width = template.match(/\bwidth="([^"]+)"/)?.[1];
  assert.match(width || '', /^(?:auto|[0-9]+(?:\.[0-9]+)?(?:px|rpx|rem)?)$/);
});

test('首次打开不重置未初始化插件，验证成功与关闭均结束等待', async () => {
  let opened = 0, resets = 0, solved = false, stale = false;
  const widget = { popup() { opened++; stale = solved; }, reset() { if (!opened) throw new Error('Cannot read properties of null (reading reset)'); resets++; solved = false; } };
  let definition: any, rendered: any, handle: any;
  vm.runInNewContext(fs.readFileSync(new URL('../src/native/captcha/index.js', import.meta.url), 'utf8'), { Component(value: unknown) { definition = value; } });
  const wrapper = { ...definition.methods, selectComponent() { return widget; }, triggerEvent(name: string, detail: unknown) { rendered.props[name === 'verify' ? 'onVerify' : 'onClose']({ detail }); } };
  const exports: any = {};
  const code = ts.transpileModule(fs.readFileSync(new URL('../src/platform/captcha.weapp.tsx', import.meta.url), 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true } }).outputText;
  vm.runInNewContext(code, { exports, setTimeout, clearTimeout, require(name: string) {
    if (name === '@tarojs/taro') return { __esModule: true, default: { getCurrentPages: () => [{ selectComponent: () => wrapper }] } };
    if (name === 'react') return { forwardRef: (f: unknown) => f, useEffect() {}, useRef: () => ({ current: undefined }), useImperativeHandle: (_: unknown, factory: () => unknown) => { handle = factory(); } };
    if (name === 'react/jsx-runtime') return { jsx: (type: string, props: unknown) => ({ type, props }) };
    if (name === '../domain/http') return { ApiError: class extends Error { constructor(_code: number, message: string) { super(message); } } };
    throw new Error('Unexpected import: ' + name);
  } });
  rendered = exports.Captcha({ config: { enabled: true, provider: 'yidun', public_config: { mini_program_captcha_id: 'fixture-id' } } }, {});
  const result = handle.verify().catch((error: Error) => error);
  if (!opened) { await result; assert.fail('首次验证没有调用 popup'); }
  solved = true; wrapper.onVerify({ detail: [null, 'fixture-proof'] });
  assert.equal((await result).token, 'fixture-proof');
  const cancelled = handle.verify(); wrapper.onClose();
  await assert.rejects(cancelled, /验证已取消/);
  assert.equal(opened, 2);
  assert.equal(resets, 1, '业务登录失败后再次验证，必须重置已成功的验证码');
  assert.equal(stale, false, '第二次不能展示上次成功的勾选状态');
});
