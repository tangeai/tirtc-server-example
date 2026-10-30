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

function editor(previous = {}) {
  const fields = new Map();
  const element = id => {
    if (!fields.has(id)) fields.set(id, {
      value: '', checked: false, disabled: false, options: [],
      classList: { add() {}, remove() {}, toggle() {} },
      add(option) { this.options.push(option); },
    });
    return fields.get(id);
  };
  const requests = [];
  const messages = [];
  const context = { WebI18n: require("../../tests/helpers/web-i18n.cjs")(),
    $: element, agents: previous.id ? [previous] : [],
    currentAgentId: previous.id || null, isEditingDefault: false,
    selectedMcpTools: ['mcp-1'], selectedPlugins: [], editorDirty: true,
    voices: [],
    Option: function(text, value) { this.text = text; this.value = value; },
    renderAgentList() {}, updateHeaderBinding() {}, updateBindButton() {},
    toast(message, error) { messages.push({ message, error }); },
    async api(path, options) {
      const body = JSON.parse(options.body);
      requests.push({ path, method: options.method, body });
      return { code: 200, data: { id: previous.id || 'created', ...body } };
    },
  };
  element('field-name').value = '角色';
  element('field-model').value = 'qwen3.7-plus';
  element('field-rate').value = '1.2';
  element('field-pitch').value = '0.9';
  element('field-voice').value = 'voice-1';
  element('field-knowledge-index').value = 'index-1';
  vm.createContext(context);
  vm.runInContext(source('persistAgent') + '\n' + source('setSelectValue'), context);
  return { context, element, requests, messages };
}

test('saving a role retains parent, complete service config and explicit cleared references', async () => {
  const previous = {
    id: 'role-1', avatar: 'avatar', parent_role_id: 'parent',
    agent_config: { ali_memory: { enable: true }, device_plugins: ['old-plugin'] },
    service_config: { name: 'service', type: 'custom-compose', config: {
      asr: { provider: 'saved-asr' },
      llm: { provider: 'ali-llm', provider_params: { model: 'old-model', temperature: 0.5 } },
      tts: { provider: 'ali-tts', provider_params: { voice: 'old', volume: 75, language_hints: ['zh'] } },
    } },
    user_params: { custom: 'preserved' },
  };
  const e = editor(previous);
  await e.context.persistAgent();
  const body = e.requests[0].body;
  assert.equal(body.parent_role_id, 'parent');
  assert.equal(body.avatar, 'avatar');
  assert.equal(body.service_config.name, 'service');
  assert.equal(body.service_config.config.llm.provider_params.model, 'qwen3.7-plus');
  assert.equal(body.service_config.config.llm.provider_params.temperature, 0.5);
  assert.equal(body.service_config.config.tts.provider_params.volume, 75);
  assert.deepEqual(body.service_config.config.asr, previous.service_config.config.asr);
  assert.deepEqual(body.agent_config.device_plugins, []);
  assert.equal(body.agent_config.ali_memory.enable, false);
  assert.equal(body.agent_config.ali_rag.index_id, 'index-1');
  assert.equal(body.agent_config.prompt, '');
  assert.equal(body.user_params.custom, 'preserved');
});

for (const type of ['coze', 'qwen-agent']) {
  test(`editing ${type} retains its service and credentials`, async () => {
    const service = { name: 'existing', type, config: { app_id: 'app', api_key: 'placeholder', workspace_id: 'workspace' } };
    const e = editor({ id: 'role-1', service_config: service });
    await e.context.persistAgent();
    assert.deepEqual(e.requests[0].body.service_config, service);
  });
}

test('inherited model is omitted instead of using another role selection', async () => {
  const e = editor({ id: 'role-1', service_config: { name: 'default', type: 'custom-compose', config: {} } });
  e.element('field-model').value = '';
  e.element('field-knowledge-index').value = '';
  await e.context.persistAgent();
  assert.equal(e.requests[0].body.service_config.config.llm, undefined);
  assert.equal(e.requests[0].body.agent_config.ali_rag, null);
});

test('saved model and knowledge selection survive missing options, then reset for an inherited value', () => {
  const e = editor();
  for (const id of ['field-model', 'field-voice', 'field-knowledge-index']) {
    e.context.setSelectValue(id, 'saved-value');
    assert.equal(e.element(id).value, 'saved-value');
    assert.equal(e.element(id).options.length, 1);
    e.context.setSelectValue(id, '');
    assert.equal(e.element(id).value, '');
  }
});

test('MCP editing preserves the transport configuration', async () => {
  const e = editor();
  Object.assign(e.context, {
    editingMcpToolId: 'mcp-1',
    editingMcpConfig: { type: 'streamableHttp', enable: false },
    closeMcpToolModal() {}, async loadMcpTools() {},
  });
  e.element('mcp-modal-name').value = 'weather';
  e.element('mcp-modal-url').value = 'https://example.com/mcp';
  vm.runInContext(source('saveMcpToolModal'), e.context);
  await e.context.saveMcpToolModal();
  assert.equal(e.requests[0].body.config.type, 'streamableHttp');
  assert.equal(e.requests[0].body.config.enable, false);
});

test('knowledge documents load subsequent pages and reject a failed page', async () => {
  const paths = [];
  let failSecondPage = false;
  const context = { WebI18n: require("../../tests/helpers/web-i18n.cjs")(), async api(path) {
    paths.push(path);
    if (path.includes('page=2')) {
      return failSecondPage ? { code: 50200 } : { code: 200, data: { items: [{ document_id: 'last' }], total: 2 } };
    }
    return { code: 200, data: { items: [{ document_id: 'first' }], total: 2 } };
  } };
  vm.createContext(context);
  vm.runInContext(source('fetchKnowledgeDocuments'), context);
  const items = await context.fetchKnowledgeDocuments('index-1');
  assert.equal(items.length, 2);
  assert.equal(items[1].document_id, 'last');
  assert.ok(paths[1].includes('page=2&page_size=100'));
  failSecondPage = true;
  await assert.rejects(context.fetchKnowledgeDocuments('index-1'), /读取知识库文档失败/);
});

test('ali-tts cannot save a voice from the ve-bidirection catalog', async () => {
  const e = editor({ id: 'role-1', service_config: { name: 'default', type: 'custom-compose' } });
  e.context.voices = [{ id: 'incompatible-voice', model: 've-bidirection' }];
  e.element('field-voice').value = 'incompatible-voice';
  await e.context.persistAgent();
  assert.equal(e.requests.length, 0, 'must ask for a compatible voice before writing');
});

for (const previous of [{}, { id: 'role-1' }]) {
  test(`ali-tts ${previous.id ? 'update' : 'create'} sends both model and voice required by the cloud`, async () => {
    const e = editor(previous);
    e.context.voices = [{ id: 'longanli_v3', model: 'cosyvoice-v3-flash' }];
    e.element('field-voice').value = 'longanli_v3';
    await e.context.persistAgent();
    const params = e.requests[0].body.service_config.config.tts.provider_params;
    assert.equal(params.model, 'cosyvoice-v3-flash', 'cloud rejects missing TTS model with HTTP 400: model和voice不能为空');
    assert.equal(params.voice, 'longanli_v3');
  });
}

test('unselected voice inherits TTS instead of sending an invalid component without voice', async () => {
  const e = editor();
  e.element('field-voice').value = '';
  await e.context.persistAgent();
  assert.equal(e.requests[0].body.service_config.config.tts, undefined);
});

for (const tc of [
  { name: 'new role with temporarily unavailable catalog', previous: {} },
  { name: 'legacy role missing TTS model', previous: { id: 'role-1' } },
  { name: 'existing role with temporarily unavailable catalog', previous: { id: 'role-1' }, savedModel: 'cosyvoice-v3-flash' },
]) {
  test(`${tc.name} sends a complete TTS pair`, async () => {
    const previous = { ...tc.previous, service_config: { name: 'default', type: 'custom-compose', config: {
      tts: { provider: 'ali-tts', provider_params: { voice: 'longanli_v3', model: tc.savedModel, volume: 70 } },
    } } };
    const e = editor(previous);
    e.element('field-voice').value = 'longanli_v3';
    await e.context.persistAgent();
    const params = e.requests[0].body.service_config.config.tts.provider_params;
    assert.equal(params.model, 'cosyvoice-v3-flash');
    assert.equal(params.voice, 'longanli_v3');
    assert.equal(e.requests[0].method, previous.id ? 'PUT' : 'POST');
    if (previous.id) assert.equal(params.volume, 70);
  });
}

test('changing voice uses its catalog model instead of the previous incompatible model', async () => {
  const e = editor({ id: 'role-1', service_config: { name: 'default', type: 'custom-compose', config: {
    tts: { provider: 'ali-tts', provider_params: { model: 've-bidirection', voice: 'old-voice', volume: 60, language_hints: ['zh'] } },
  } } });
  e.context.voices = [{ id: 'longanli_v3', model: 'cosyvoice-v3-flash' }];
  e.element('field-voice').value = 'longanli_v3';
  await e.context.persistAgent();
  const params = e.requests[0].body.service_config.config.tts.provider_params;
  assert.equal(params.model, 'cosyvoice-v3-flash');
  assert.equal(params.voice, 'longanli_v3');
  assert.equal(params.volume, 60);
  assert.deepEqual(params.language_hints, ['zh']);
});

test('clearing an existing TTS selection inherits service without mutating the unsaved role', async () => {
  const previous = { id: 'role-1', parent_role_id: 'parent', service_config: { name: 'service', type: 'custom-compose', config: {
    tts: { provider: 'ali-tts', provider_params: { model: 'cosyvoice-v3-flash', voice: 'old-voice' } },
  } }, user_params: { voice: 'old-voice', custom: 'kept' } };
  const e = editor(previous);
  e.element('field-voice').value = '';
  await e.context.persistAgent();
  const body = e.requests[0].body;
  assert.equal(body.service_config.config.tts, undefined);
  assert.equal(body.user_params.voice, undefined);
  assert.equal(body.user_params.custom, 'kept');
  assert.equal(body.parent_role_id, 'parent');
  assert.equal(previous.service_config.config.tts.provider_params.voice, 'old-voice');
});

for (const model of ['ve-bidirection', 'seed-tts-2.0']) {
  test(`creating a role with ${model} voice makes no API request and keeps edits`, async () => {
    const e = editor();
    e.context.voices = [{ id: 'other-voice', model }];
    e.element('field-voice').value = 'other-voice';
    await e.context.persistAgent();
    assert.equal(e.requests.length, 0);
    assert.equal(e.context.editorDirty, true);
    assert.equal(e.element('field-voice').value, 'other-voice');
    assert.equal(e.messages.at(-1).error, true);
  });
}

for (const previous of [{}, { id: 'role-1', name: 'saved name' }]) {
  test(`${previous.id ? 'update' : 'create'} failure retains form and dirty state for retry`, async () => {
    const e = editor(previous);
    e.context.api = async () => ({ code: 50200, msg: '保存失败，请稍后重试' });
    e.element('field-welcome').value = 'unsaved greeting';
    await e.context.persistAgent();
    assert.equal(e.context.editorDirty, true);
    assert.equal(e.context.currentAgentId, previous.id || null);
    assert.equal(e.element('field-welcome').value, 'unsaved greeting');
    assert.equal(previous.name, previous.id ? 'saved name' : undefined);
    assert.equal(e.messages.at(-1).error, true);
  });
}

test('double save while a request is pending sends once, and timeout restores the save button', async () => {
  const e = editor({ id: 'role-1' });
  Object.assign(e.context, { savingRole: false, bindingRole: false, selectingRole: 0 });
  vm.runInContext(source('saveAgent'), e.context);
  let calls = 0;
  let reject;
  e.context.api = () => {
    calls++;
    return new Promise((resolve, rejectRequest) => { reject = rejectRequest; });
  };
  const pending = e.context.saveAgent();
  assert.equal(e.element('btn-save').disabled, true);
  await e.context.saveAgent();
  assert.equal(calls, 1);
  reject(new Error('request timeout'));
  await pending;
  assert.equal(e.element('btn-save').disabled, false);
  assert.equal(e.context.savingRole, false);
  assert.equal(e.context.editorDirty, true);
  assert.equal(e.messages.at(-1).error, true);
});
