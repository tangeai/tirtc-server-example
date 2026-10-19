// Serve thing-connect on localhost:18743, then run with playwright-cli run-code.
// All API traffic uses fixtures; no production role is modified.
async (page) => {
  const base = 'http://127.0.0.1:18743';
  await page.unrouteAll();
  await page.addInitScript(() => localStorage.setItem('token', 'browser-test-token'));
  const errors = [];
  const writes = [];
  page.on('pageerror', error => errors.push(error.message));
  const service = { name: 'service', type: 'custom-compose', config: {
    llm: { provider: 'ali-llm', provider_params: { model: 'saved-model' } },
    tts: { provider: 'ali-tts', provider_params: { voice: 'ali-voice', volume: 75 } },
  } };
  const roles = [
    { id: 'custom', name: '自定义角色', parent_role_id: 'parent', service_config: service },
    { id: 'coze', name: 'Coze 角色', service_config: { name: 'coze', type: 'coze', config: { bot_id: 'bot', api_key: 'placeholder' } } },
    { id: 'inherited', name: '继承角色', service_config: { name: 'base', type: 'custom-compose', config: {} } },
    { id: 'badvoice', name: '历史音色角色', service_config: { ...service, config: { ...service.config, tts: { provider: 'ali-tts', provider_params: { voice: 've-voice' } } } } },
  ];
  await page.route('**/static/**', async route => {
    const path = new URL(route.request().url()).pathname;
    if (!path.startsWith('/static/')) return route.continue();
    const response = await route.fetch({ url: base + '/user-server' + path });
    await route.fulfill({ response });
  });
  await page.route('**/v1/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (['POST', 'PUT'].includes(request.method()) && path.startsWith('/v1/ai/roles')) {
      const params = request.postDataJSON().service_config?.config?.tts?.provider_params;
      if (params && (!params.model || !params.voice)) {
        await route.fulfill({ status: 502, json: { code: 50200, msg: '创建角色失败：AI 云服务暂不可用，请稍后重试' } });
        return;
      }
    }
    let data = { items: [] };
    if (path.endsWith('/device/test-device/role')) data = { role_id: 'custom' };
    else if (path === '/v1/ai/roles' && request.method() === 'POST') {
      const body = request.postDataJSON();
      writes.push({ id: 'created', body });
      data = { id: 'created', ...body };
      roles.push(data);
    } else if (path === '/v1/ai/roles') data = { items: roles, total: roles.length };
    else if (path.startsWith('/v1/ai/roles/')) {
      const role = roles.find(item => item.id === path.split('/').pop());
      if (request.method() === 'PUT') {
        const body = request.postDataJSON();
        writes.push({ id: role.id, body });
        Object.assign(role, body);
      }
      data = role;
    } else if (path === '/v1/ai/voices') data = { items: [
      { id: 'ali-voice', name: '阿里音色', model: 'cosyvoice-v3-flash', languages: ['zh'] },
      { id: 've-voice', name: '其他引擎音色', model: 've-bidirection', languages: ['zh'] },
    ] };
    await route.fulfill({ json: { code: 200, data } });
  });
  const check = (ok, message) => { if (!ok) throw Error(message); };
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(base + '/ai-server/static/agent.html?device_id=test-device');
  await page.waitForFunction(() => document.getElementById('field-name').value === '自定义角色');
  check(await page.locator('#field-model').inputValue() === 'saved-model', 'saved model lost');
  check(await page.locator('#field-voice option[value="ve-voice"]').count() === 0, 'incompatible voice offered');
  await page.locator('#btn-save').click();
  await page.waitForFunction(() => document.getElementById('toast').textContent === '保存成功');
  check(writes[0].body.parent_role_id === 'parent', 'parent lost');
  check(writes[0].body.service_config.config.tts.provider_params.volume === 75, 'volume lost');
  check(writes[0].body.service_config.config.tts.provider_params.model === 'cosyvoice-v3-flash', 'TTS model missing');
  await page.locator('[data-role-id="coze"]').click();
  await page.waitForFunction(() => document.getElementById('field-name').value === 'Coze 角色');
  check(await page.locator('#field-model').isDisabled(), 'coze model editable');
  await page.locator('#field-desc').fill('更新描述');
  await page.locator('#btn-save').click();
  await page.waitForFunction(() => !document.getElementById('btn-save').disabled);
  check(writes[1].body.service_config.type === 'coze', 'coze service replaced');
  check(writes[1].body.service_config.config.api_key === 'placeholder', 'coze config lost');
  await page.locator('[data-role-id="inherited"]').click();
  await page.waitForFunction(() => document.getElementById('field-name').value === '继承角色');
  check(await page.locator('#field-model').isEnabled(), 'model stays disabled after coze');
  check(await page.locator('#field-model').inputValue() === '', 'inherited model uses stale value');
  await page.locator('[data-role-id="badvoice"]').click();
  await page.waitForFunction(() => document.getElementById('field-name').value === '历史音色角色');
  check(await page.locator('#field-voice').inputValue() === 've-voice', 'saved incompatible voice silently changed');
  await page.locator('#btn-save').click();
  await page.waitForFunction(() => document.getElementById('toast').textContent.includes('重新选择音色'));
  check(writes.length === 2, 'incompatible voice submitted');
  await page.locator('#field-voice').selectOption('ali-voice');
  await page.locator('#btn-save').click();
  await page.waitForFunction(() => document.getElementById('toast').textContent === '保存成功');
  check(writes[2].body.service_config.config.tts.provider_params.voice === 'ali-voice', 'corrected voice not submitted');
  await page.setViewportSize({ width: 360, height: 900 });
  await page.locator('[data-role-id="badvoice"]').click();
  await page.locator('#editor-form').waitFor({ state: 'visible' });
  check(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile editor overflows');
  await page.getByRole('button', { name: '返回列表', exact: true }).click();
  await page.getByRole('button', { name: '新建智能体' }).click();
  await page.locator('#field-name').fill('手机创建角色');
  await page.locator('#field-model').selectOption('qwen3.7-plus');
  await page.locator('#field-voice').selectOption('ali-voice');
  await page.locator('#field-welcome').fill('你好');
  await page.locator('#btn-save').click();
  await page.waitForFunction(() => document.getElementById('toast').textContent === '创建成功');
  check(writes.length === 4 && writes[3].id === 'created', 'mobile create did not submit exactly once');
  check(writes[3].body.service_config.config.tts.provider_params.model === 'cosyvoice-v3-flash', 'create omitted TTS model');
  check(writes[3].body.service_config.config.tts.provider_params.voice === 'ali-voice', 'create omitted TTS voice');
  check(writes[3].body.service_config.config.llm.provider_params.model === 'qwen3.7-plus', 'LLM model confused with TTS model');
  check(errors.length === 0, errors.join('\n'));
  console.log('PASS: desktop update and mobile create include TTS model/voice; config preservation, service switching and incompatible voice rejection; 4 fixture writes');
}
