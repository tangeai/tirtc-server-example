import assert from 'node:assert/strict';
import test from 'node:test';
import { checkSource } from './check-ui.mjs';

const check = (source, filename = 'src/pages/example.tsx') => checkSource(source, filename);

test('accepts labeled actions, dynamic icons and imported aliases', () => {
  assert.deepEqual(
    check(`
    import { Button as Action } from 'antd';
    <Action icon={enabled ? <StopOutlined /> : <CheckOutlined />}>
      {enabled ? '停用' : '启用'}
    </Action>;
    <Action icon={<SaveOutlined />}><span>保存</span></Action>;
  `),
    [],
  );
});

test('rejects missing icons, including aliased and namespace buttons', () => {
  for (const source of [
    `import { Button } from 'antd'; <Button>保存</Button>;`,
    `import { Button as Action } from 'antd'; <Action icon={null}>保存</Action>;`,
    `import * as Ant from 'antd'; <Ant.Button icon={undefined}>保存</Ant.Button>;`,
  ]) {
    assert.match(check(source).join('\n'), /必须提供非空 icon/);
  }
});

test('accessible names and icons alone do not replace visible labels', () => {
  for (const body of [
    `<Button aria-label="删除" icon={<DeleteOutlined />} />`,
    `<Button icon={<DeleteOutlined />}>{null}</Button>`,
    `<Button icon={<DeleteOutlined />}><span> </span></Button>`,
  ]) {
    assert.match(check(`import { Button } from 'antd'; ${body}`).join('\n'), /必须提供可见文案/);
  }
});

test('refresh belongs to the shared component; unrelated similarly named components are ignored', () => {
  const source = `import { Button } from 'antd'; <Button icon={<ReloadOutlined />}>刷新页面</Button>`;
  assert.match(check(source).join('\n'), /复用 RefreshButton/);
  assert.deepEqual(check(source.replace('刷新页面', '刷新'), 'src/shared/admin-ui.tsx'), []);
  assert.deepEqual(check(`import { Button } from './diagram'; <Button />`), []);
});

test('query forms use shared actions with explicit loading', () => {
  const imports = `
    import { QueryFilter as Filter } from '@ant-design/pro-components';
    import { queryActions as actions } from '../../shared/admin-ui';
  `;
  assert.deepEqual(check(`${imports}<Filter submitter={actions(loading)} />`), []);
  for (const attributes of ['', 'submitter={{}}', 'submitter={actions()}']) {
    assert.match(check(`${imports}<Filter ${attributes} />`).join('\n'), /queryActions\(loading\)/);
  }
});

test('reports locations and rejects native business buttons', () => {
  assert.match(check('\n<button>保存</button>')[0], /^src\/pages\/example.tsx:2:1 /);
  assert.deepEqual(check('<Modal closable /><Input allowClear /><Table />'), []);
});
