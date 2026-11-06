import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LatestIntent } from './latest-intent.ts';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
test('one click opens after a slow SDK load; no second click or reset required', async () => {
  const ready = deferred<number>(); let opened = 0;
  const gate = new LatestIntent(() => ready.promise);
  const pending = gate.run(() => { opened++; });
  ready.resolve(1); await pending; assert.equal(opened, 1);
});
test('rapid clicks share initialization and only the latest intent opens', async () => {
  const ready = deferred<number>(); let loaded = 0;
  const gate = new LatestIntent(() => { loaded++; return ready.promise; });
  const actions: string[] = [];
  const first = gate.run(() => { actions.push('old'); });
  const second = gate.run(() => { actions.push('new'); });
  ready.resolve(1); await Promise.all([first, second]);
  assert.equal(loaded, 1); assert.deepEqual(actions, ['new']);
});
test('changing email cancels an old click but a new click during loading still opens', async () => {
  const ready = deferred<number>(); const actions: string[] = [];
  const gate = new LatestIntent(() => ready.promise);
  const first = gate.run(() => { actions.push('old email'); });
  gate.cancel(); const second = gate.run(() => { actions.push('new email'); });
  ready.resolve(1); await Promise.all([first, second]); assert.deepEqual(actions, ['new email']);
});
test('changing forms without another click does not open a stale challenge', async () => {
  const ready = deferred<number>(); let opened = false;
  const gate = new LatestIntent(() => ready.promise);
  const pending = gate.run(() => { opened = true; }); gate.cancel(); ready.resolve(1);
  await pending; assert.equal(opened, false);
});
test('failed initialization can be retried', async () => {
  let calls = 0; let opened = false;
  const gate = new LatestIntent(async () => { if (++calls === 1) throw new Error('offline'); return 1; });
  await assert.rejects(gate.run(() => {}));
  await gate.run(() => { opened = true; }); assert.equal(opened, true); assert.equal(calls, 2);
});
