import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs';
import vm from 'node:vm';
import ts from 'typescript';

test('登录页首次加载配置时显示等待，不显示失败重试入口', () => {
  const exports: any = {};
  const code = ts.transpileModule(fs.readFileSync(new URL('../src/pages/login/index.tsx', import.meta.url), 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true } }).outputText;
  const jsx = (type: unknown, props: unknown) => ({ type, props });
  vm.runInNewContext(code, { exports, require(name: string) {
    if (name === 'react/jsx-runtime') return { jsx, jsxs: jsx };
    if (name === 'react') return { useState: (value: unknown) => [value, () => {}], useRef: (value: unknown) => ({ current: value }), useEffect() {} };
    if (name === '@tarojs/taro') return { __esModule: true, default: {}, useLoad() {} };
    if (name === '@tarojs/components') return { Button: 'button', Checkbox: 'checkbox', Input: 'input', Text: 'text', View: 'view' };
    return {};
  } });
  const nodes: any[] = [];
  function walk(node: any) { if (!node || typeof node !== 'object') return; if (Array.isArray(node)) return node.forEach(walk); nodes.push(node); walk(node.props?.children); }
  walk(exports.default());
  assert.equal(nodes.some(n => n.props?.className === 'auth-retry'), false, '配置请求尚未失败，不能提示重新加载');
  const submit = nodes.find(n => n.props?.className === 'auth-submit');
  assert.equal(submit.props.disabled, true);
  assert.match(submit.props.children, /加载/);
});
