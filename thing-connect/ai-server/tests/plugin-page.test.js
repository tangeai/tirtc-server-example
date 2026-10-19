const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const html = fs.readFileSync(__dirname + '/../static/agent.html', 'utf8');
function source(name) {
  const match = html.match(new RegExp(`((?:async )?function ${name}\\([\\s\\S]*?)(?=\\n(?:async )?function |\\n</script>)`));
  assert.ok(match, `missing function ${name}`);
  return match[1];
}

function pluginEditor(api) {
  const fields = new Map();
  const element = id => {
    if (!fields.has(id)) {
      const classes = new Set(['hidden']);
      fields.set(id, { value: '', textContent: '', innerHTML: '',
        classList: { add(name) { classes.add(name); }, remove(name) { classes.delete(name); }, contains(name) { return classes.has(name); } },
        insertAdjacentHTML(position, content) { this.innerHTML += content; },
      });
    }
    return fields.get(id);
  };
  const messages = [];
  const context = { $: element, api, toast(message, error) { messages.push({ message, error }); },
    pluginTools: [], selectedPlugins: [], isEditingDefault: false, defaultRole: null,
    mcpTools: [], selectedMcpTools: [], mcpLoadError: false,
    editingPluginId: null, pluginModalRequest: 0, pluginLoadError: false,
    pluginModalInputParams: [], pluginModalReturnParams: [],
    renderParamTable() {},
  };
  vm.createContext(context);
  vm.runInContext(['escapeText', 'roleResourceItems', 'loadPlugins', 'renderPlugins', 'loadMcpTools', 'renderMcpTools', 'toggleMcp', 'togglePlugin', 'openPluginModal', 'closePluginModal', 'cleanParams', 'savePluginModal'].map(source).join('\n'), context);
  return { context, element, messages };
}

for (const result of [{ code: 50200, msg: '查询失败' }, { code: 40400, msg: '插件不存在' }, { code: 200, data: null }]) {
  test(`plugin edit never opens an empty create form after detail failure ${result.code}`, async () => {
    const e = pluginEditor(async () => result);
    await e.context.openPluginModal('plugin-1');
    assert.equal(e.element('plugin-modal').classList.contains('hidden'), true);
    assert.equal(e.context.editingPluginId, null);
    assert.equal(e.messages.at(-1).error, true);
  });
}

test('plugin edit handles a network failure without opening or clearing a form', async () => {
  const e = pluginEditor(async () => { throw Error('network failed'); });
  e.element('plugin-modal-name').value = 'previous input';
  await e.context.openPluginModal('plugin-1');
  assert.equal(e.element('plugin-modal-name').value, 'previous input');
  assert.equal(e.element('plugin-modal').classList.contains('hidden'), true);
  assert.equal(e.context.editingPluginId, null);
  assert.equal(e.messages.at(-1).error, true);
});

test('plugin editing fetches detail, retains parameter enum and sends PUT with an update notice', async () => {
  const writes = [];
  const detail = { id: 'plugin-1', name: 'Light', action: 'set_light', description: 'description',
    input_params: [{ name: 'mode', type: 'string', required: false, enum: ['day', 'night'], default_value: 'day' }], return_params: [],
  };
  const e = pluginEditor(async (path, options) => {
    if (options?.method === 'PUT') {
      const body = JSON.parse(options.body);
      writes.push({ path, body });
      return { code: 200, data: { id: 'plugin-1', ...body } };
    }
    return { code: 200, data: path.endsWith('/plugin-1') ? detail : { items: [{ id: 'plugin-1', name: 'Renamed' }] } };
  });
  await e.context.openPluginModal('plugin-1');
  assert.equal(e.element('plugin-modal-title').textContent, '编辑设备端插件');
  assert.equal(e.element('plugin-modal-action').value, 'set_light');
  e.element('plugin-modal-name').value = 'Renamed';
  await e.context.savePluginModal();
  assert.equal(writes.length, 1);
  assert.equal(writes[0].path, '/v1/ai/plugins/plugin-1');
  assert.deepEqual(writes[0].body.input_params[0].enum, ['day', 'night']);
  assert.equal(writes[0].body.action, 'set_light');
  assert.equal(e.messages.at(-1).message, '插件已更新');
  assert.equal(e.element('plugin-modal').classList.contains('hidden'), true);
});

test('plugin list failure keeps existing entries and reports an error instead of an empty list', async () => {
  const e = pluginEditor(async () => ({ code: 50200 }));
  e.context.pluginTools = [{ id: 'plugin-1', name: 'Light' }];
  await e.context.loadPlugins();
  assert.equal(e.context.pluginTools.length, 1);
  assert.equal(e.messages.at(-1).error, true);
});

test('initial plugin list failure is distinguished from a successful empty list', async () => {
  const e = pluginEditor(async () => ({ code: 50000 }));
  await e.context.loadPlugins();
  assert.match(e.element('plugin-tools-list').innerHTML, /加载失败/);
  assert.doesNotMatch(e.element('plugin-tools-list').innerHTML, /暂无设备插件/);
  e.context.api = async () => ({ code: 200, data: { items: [] } });
  await e.context.loadPlugins();
  assert.match(e.element('plugin-tools-list').innerHTML, /暂无设备插件/);
});

test('plugin list exposes a readable edit button and escapes plugin names', () => {
  const e = pluginEditor();
  e.context.pluginTools = [{ id: 'plugin-1', name: '<b>Light</b>' }];
  e.context.renderPlugins();
  assert.match(e.element('plugin-tools-list').innerHTML, />编辑<\/button>/);
  assert.ok(!e.element('plugin-tools-list').innerHTML.includes('<b>Light</b>'));
  e.context.isEditingDefault = true;
  e.context.renderPlugins();
  assert.doesNotMatch(e.element('plugin-tools-list').innerHTML, /openPluginModal/);
});

test('late plugin detail cannot overwrite a newer editor selection', async () => {
  let finishFirst;
  const e = pluginEditor(path => path.endsWith('/first')
    ? new Promise(resolve => { finishFirst = resolve; })
    : Promise.resolve({ code: 200, data: { id: 'second', name: 'Second', action: 'second' } }));
  const first = e.context.openPluginModal('first');
  await e.context.openPluginModal('second');
  finishFirst({ code: 200, data: { id: 'first', name: 'First', action: 'first' } });
  await first;
  assert.equal(e.context.editingPluginId, 'second');
  assert.equal(e.element('plugin-modal-name').value, 'Second');
});

test('default role displays all linked plugins even when the local catalog is empty', () => {
  const e = pluginEditor();
  e.context.isEditingDefault = true;
  e.context.selectedPlugins = ['cloud-plugin-1', 'cloud-plugin-2', 'cloud-plugin-1'];
  e.context.renderPlugins();
  const content = e.element('plugin-tools-list').innerHTML;
  assert.match(content, /cloud-plugin-1/);
  assert.match(content, /cloud-plugin-2/);
  assert.equal((content.match(/已关联/g) || []).length, 2);
  assert.doesNotMatch(content, /暂无设备插件|openPluginModal|deletePlugin|togglePlugin/);
});

test('default role resolves known names and excludes plugins unrelated to this role', () => {
  const e = pluginEditor();
  e.context.isEditingDefault = true;
  e.context.selectedPlugins = ['linked'];
  e.context.pluginTools = [{ id: 'linked', name: '默认灯光' }, { id: 'unrelated', name: '私人插件' }];
  e.context.renderPlugins();
  assert.match(e.element('plugin-tools-list').innerHTML, /默认灯光/);
  assert.doesNotMatch(e.element('plugin-tools-list').innerHTML, /私人插件/);
  e.context.selectedPlugins = [];
  e.context.renderPlugins();
  assert.match(e.element('plugin-tools-list').innerHTML, /未关联设备插件/);
});

test('catalog failure still shows default role associations with an explicit failure notice', async () => {
  const e = pluginEditor(async () => ({ code: 50200 }));
  e.context.isEditingDefault = true;
  e.context.selectedPlugins = ['linked'];
  await e.context.loadPlugins();
  assert.match(e.element('plugin-tools-list').innerHTML, /linked/);
  assert.match(e.element('plugin-tools-list').innerHTML, /加载失败/);
  assert.doesNotMatch(e.element('plugin-tools-list').innerHTML, /暂无设备插件/);
});

test('custom role keeps unknown associations visible without exposing resource edit actions', () => {
  const e = pluginEditor();
  e.context.selectedPlugins = ['unknown'];
  e.context.renderPlugins();
  const content = e.element('plugin-tools-list').innerHTML;
  assert.match(content, /unknown/);
  assert.match(content, /togglePlugin/);
  assert.doesNotMatch(content, /title="编辑设备插件"|deletePlugin/);
  assert.deepEqual(e.context.selectedPlugins, ['unknown']);
});

test('default role shows linked MCP tools only, including absent catalog entries, and is read-only', () => {
  const e = pluginEditor();
  e.context.isEditingDefault = true;
  e.context.mcpTools = [{ id: 'known', name: '天气', source: 'global' }, { id: 'private', name: '私人 MCP', source: 'app' }];
  e.context.selectedMcpTools = ['known', 'cloud-mcp', 'cloud-mcp'];
  e.context.renderMcpTools();
  const content = e.element('mcp-tools-list').innerHTML;
  assert.match(content, /天气/);
  assert.match(content, /cloud-mcp/);
  assert.equal((content.match(/已关联/g) || []).length, 2);
  assert.doesNotMatch(content, /私人 MCP|openMcpToolModal|deleteMcpTool|toggleMcp/);
  e.context.toggleMcp('known');
  e.context.selectedPlugins = ['plugin'];
  e.context.togglePlugin('plugin');
  assert.deepEqual(e.context.selectedMcpTools, ['known', 'cloud-mcp', 'cloud-mcp']);
  assert.deepEqual(e.context.selectedPlugins, ['plugin']);
});

test('custom role can associate public MCP tools and manage user MCP tools', () => {
  const e = pluginEditor();
  e.context.mcpTools = [{ id: 'public', name: '公共 MCP', source: 'global' }, { id: 'own', name: '用户 MCP', source: 'app' }];
  e.context.selectedMcpTools = ['unknown'];
  e.context.renderMcpTools();
  const content = e.element('mcp-tools-list').innerHTML;
  assert.match(content, /unknown/);
  assert.match(content, /openMcpToolModal\((?:'|&quot;)own/);
  assert.doesNotMatch(content, /openMcpToolModal\((?:'|&quot;)(public|unknown)/);
  e.context.toggleMcp('own');
  assert.deepEqual(e.context.selectedMcpTools, ['unknown', 'own']);
});

test('MCP partial catalog failure preserves successful results and linked references', async () => {
  const e = pluginEditor(async path => {
    if (path.endsWith('/app-tools')) throw Error('network unavailable');
    return { code: 200, data: { items: [{ id: 'global', name: '公共工具' }] } };
  });
  e.context.isEditingDefault = true;
  e.context.selectedMcpTools = ['global', 'linked'];
  await e.context.loadMcpTools();
  const content = e.element('mcp-tools-list').innerHTML;
  assert.match(content, /公共工具/);
  assert.match(content, /linked/);
  assert.match(content, /加载失败/);
});

test('default role displays resolved plugin names without adding them to the user catalog', () => {
  const e = pluginEditor();
  e.context.isEditingDefault = true;
  e.context.selectedPlugins = ['linked', 'missing'];
  e.context.defaultRole = { device_plugin_refs: [{ id: 'linked', name: '设备呼叫' }, { id: 'missing', name: '' }] };
  e.context.renderPlugins();
  const content = e.element('plugin-tools-list').innerHTML;
  assert.match(content, /设备呼叫/);
  assert.match(content, /missing/);
  assert.doesNotMatch(content, /openPluginModal|deletePlugin/);
  e.context.isEditingDefault = false;
  e.context.selectedPlugins = [];
  e.context.renderPlugins();
  assert.doesNotMatch(e.element('plugin-tools-list').innerHTML, /设备呼叫/);
  assert.equal(e.context.pluginTools.length, 0);
});
