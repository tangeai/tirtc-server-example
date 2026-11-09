import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import ts from 'typescript';

// Run the actual client modules against platform adapters, without a live account.
export function loadSource(file: string, mocks: Record<string, unknown>, globals: Record<string, unknown> = {}) {
  const cache = new Map<string, { exports: any }>();
  function load(filename: string): any {
    if (cache.has(filename)) return cache.get(filename)!.exports;
    const module = { exports: {} }; cache.set(filename, module);
    const code = ts.transpileModule(fs.readFileSync(filename, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true } }).outputText;
    vm.runInNewContext(code, { module, exports: module.exports, setTimeout, clearTimeout, ...globals, require(name: string) {
      if (name in mocks) return mocks[name];
      if (name.startsWith('.')) {
        const base = path.resolve(path.dirname(filename), name);
        const target = [base, base + '.ts', base + '.tsx', base + '.js'].find(p => fs.existsSync(p) && fs.statSync(p).isFile());
        if (target) return load(target);
      }
      throw new Error('Unexpected dependency: ' + name);
    } }, { filename });
    return module.exports;
  }
  return load(file);
}
