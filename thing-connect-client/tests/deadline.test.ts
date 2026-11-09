import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { loadSource } from './helpers/load-source.ts';
function harness() {
  const timers = new Map<number, () => void>(); let id = 0;
  const { withDeadline } = loadSource(fileURLToPath(new URL('../src/domain/deadline.ts', import.meta.url)), {}, {
    setTimeout: (fn: () => void) => { timers.set(++id, fn); return id; }, clearTimeout: (key: number) => timers.delete(key),
  });
  return { withDeadline, timers };
}
test('原生接口无回调会超时；迟到成功不会把失败变成成功', async () => {
  const h = harness(); let resolveNative: any;
  const result = h.withDeadline((resolve: unknown) => { resolveNative = resolve; }, 10000, '授权查询超时');
  h.timers.values().next().value!(); resolveNative({ list: [] });
  await assert.rejects(result, /授权查询超时/);
});
test('成功、失败和同步异常均释放计时器', async () => {
  for (const mode of ['success', 'failure', 'throw']) {
    const h = harness();
    const result = h.withDeadline((resolve: any, reject: any) => { if (mode === 'success') resolve(42); else if (mode === 'failure') reject(new Error('拒绝')); else throw new Error('未就绪'); }, 1000, '超时');
    if (mode === 'success') assert.equal(await result, 42); else await assert.rejects(result);
    assert.equal(h.timers.size, 0);
  }
});
