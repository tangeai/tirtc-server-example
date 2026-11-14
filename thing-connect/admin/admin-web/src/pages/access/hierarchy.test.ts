import assert from 'node:assert/strict';
import test from 'node:test';
import { hierarchy, descendantIDs, activeAncestors } from './hierarchy.ts';

test('preserves directory/page hierarchy, order and each original grant ID', () => {
  const tree = hierarchy([
    { id: 3, parent_id: 1, sort_no: 20 },
    { id: 1 },
    { id: 2, parent_id: 1, sort_no: 10 },
  ]);
  assert.deepEqual(
    tree.map((r) => r.id),
    [1],
  );
  assert.deepEqual(
    tree[0].children?.map((r) => r.id),
    [2, 3],
  );
});
test('invalid legacy cycles and orphans remain visible without recursion', () => {
  assert.equal(
    hierarchy([
      { id: 1, parent_id: 2 },
      { id: 2, parent_id: 1 },
      { id: 3, parent_id: 99 },
    ]).length,
    3,
  );
});
test('parent selection excludes self and all descendants', () => {
  assert.deepEqual(
    [...descendantIDs([{ id: 1 }, { id: 2, parent_id: 1 }, { id: 3, parent_id: 2 }, { id: 4 }], 1)],
    [1, 2, 3],
  );
});
test('inheritance stops at disabled roles and cycles', () => {
  const rows = [
    { id: 1, parent_id: 2, status: 1 },
    { id: 2, parent_id: 3, status: 1 },
    { id: 3, parent_id: 1, status: 0 },
  ];
  assert.deepEqual(
    activeAncestors(rows, 1).map((r) => r.id),
    [2],
  );
  rows[2].status = 1;
  assert.deepEqual(
    activeAncestors(rows, 1).map((r) => r.id),
    [2, 3],
  );
});
