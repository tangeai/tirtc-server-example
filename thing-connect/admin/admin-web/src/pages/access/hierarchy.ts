export interface HierarchyRow {
  id: number;
  parent_id?: number;
  sort_no?: number;
}

// Keep every row visible, including legacy orphaned or cyclic records.
export function hierarchy<T extends HierarchyRow>(
  rows: T[],
): (T & { children?: ReturnType<typeof hierarchy<T>> })[] {
  const byID = new Map(rows.map((row) => [row.id, row]));
  const roots: (T & { children?: ReturnType<typeof hierarchy<T>> })[] = [];
  const nodes = new Map(
    rows.map((row) => [row.id, { ...row } as T & { children?: ReturnType<typeof hierarchy<T>> }]),
  );
  for (const row of [...rows].sort((a, b) => (a.sort_no || 0) - (b.sort_no || 0) || a.id - b.id)) {
    const seen = new Set([row.id]);
    let parent = row.parent_id;
    let cyclic = false;
    while (parent && byID.has(parent)) {
      if (seen.has(parent)) {
        cyclic = true;
        break;
      }
      seen.add(parent);
      parent = byID.get(parent)?.parent_id;
    }
    const node = nodes.get(row.id)!;
    const target = !cyclic && row.parent_id ? nodes.get(row.parent_id) : undefined;
    if (target) (target.children ||= []).push(node);
    else roots.push(node);
  }
  return roots;
}

export function descendantIDs(rows: HierarchyRow[], id?: number): Set<number> {
  const ids = new Set<number>(id ? [id] : []);
  let changed = true;
  while (changed) {
    changed = false;
    for (const row of rows)
      if (row.parent_id && ids.has(row.parent_id) && !ids.has(row.id)) {
        ids.add(row.id);
        changed = true;
      }
  }
  return ids;
}

export function activeAncestors<T extends HierarchyRow & { status: number }>(
  rows: T[],
  id: number,
): T[] {
  const result: T[] = [];
  const seen = new Set([id]);
  let parent = rows.find((row) => row.id === id)?.parent_id;
  while (parent && !seen.has(parent)) {
    seen.add(parent);
    const row = rows.find((item) => item.id === parent);
    if (!row || row.status !== 1) break;
    result.push(row);
    parent = row.parent_id;
  }
  return result;
}
