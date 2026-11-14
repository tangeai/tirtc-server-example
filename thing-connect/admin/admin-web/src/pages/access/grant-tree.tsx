import { Tree } from 'antd';
import type { TreeDataNode } from 'antd';

// A directory/group never silently grants its descendants. Only checked IDs are saved.
export function GrantTree<T extends string | number>({
  value = [],
  onChange,
  treeData,
  disabled = false,
}: {
  value?: T[];
  onChange?: (value: T[]) => void;
  treeData: TreeDataNode[];
  disabled?: boolean;
}) {
  return (
    <Tree
      checkable
      checkStrictly
      selectable={false}
      defaultExpandAll
      disabled={disabled}
      checkedKeys={value}
      treeData={treeData}
      onCheck={(keys) => onChange?.((Array.isArray(keys) ? keys : keys.checked) as T[])}
    />
  );
}
