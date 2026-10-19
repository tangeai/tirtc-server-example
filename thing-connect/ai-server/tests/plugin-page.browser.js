// Serve thing-connect on localhost:18743; all business requests use fixtures.
async (page) => {
  const base = 'http://127.0.0.1:18743';
  await page.unrouteAll();
  await page.addInitScript(() => localStorage.setItem('token', 'browser-test-token'));
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  const writes = [];
  const role = { id: 'role-1', name: '测试角色', agent_config: {}, service_config: { name: 'default', type: 'custom-compose' } };
  const defaultRole = { id: 'default-role', name: '默认角色', agent_config: {
    mcp_tools: ['global-mcp', 'cloud-mcp'], device_plugins: ['plugin-1', 'cloud-plugin'],
  }, device_plugin_refs: [{ id: 'cloud-plugin', name: '设备呼叫' }] };
  const plugin = { id: 'plugin-1', name: '灯光控制', action: 'set_light', description: '设备灯光',
    input_params: [{ name: 'mode', type: 'string', enum: ['day', 'night'], default_value: 'day' }], return_params: [] };
  let plugins = [{ id: plugin.id, name: plugin.name }, { id: 'missing', name: '云端已删除插件' }];
  await page.route('**/static/**', async route => {
    const path = new URL(route.request().url()).pathname;
    if (!path.startsWith('/static/')) return route.continue();
    const response = await route.fetch({ url: base + '/user-server' + path });
    await route.fulfill({ response });
  });
  await page.route('**/v1/**', async route => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    let data = { items: [] };
    if (path.endsWith('/device/test-device/role')) data = { role_id: role.id, default_role_id: defaultRole.id };
    else if (path === '/v1/ai/roles') data = { items: [role] };
    else if (path === '/v1/ai/roles/default') data = defaultRole;
    else if (path === '/v1/ai/roles/role-1') data = role;
    else if (path === '/v1/ai/mcp/tools') data = { items: [{ id: 'global-mcp', name: '天气查询' }, { id: 'unrelated-mcp', name: '未关联工具' }] };
    else if (path === '/v1/ai/plugins') data = { items: plugins };
    else if (path === '/v1/ai/plugins/plugin-1') {
      if (request.method() === 'PUT') {
        const body = request.postDataJSON();
        writes.push(body);
        Object.assign(plugin, body);
        plugins[0].name = body.name;
      }
      data = plugin;
    } else if (path === '/v1/ai/plugins/missing') {
      if (request.method() === 'DELETE') {
        plugins = plugins.filter(item => item.id !== 'missing');
        await route.fulfill({ json: { code: 200 } });
      } else {
        await route.fulfill({ status: 404, json: { code: 40400, msg: '设备插件不存在' } });
      }
      return;
    }
    await route.fulfill({ json: { code: 200, data } });
  });
  const check = (ok, message) => { if (!ok) throw Error(message); };
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto(base + '/ai-server/static/agent.html?device_id=test-device');
  await page.waitForFunction(() => document.getElementById('field-name').value === '测试角色');
  const pluginRow = page.locator('#plugin-tools-list > div').filter({ hasText: '灯光控制' });
  await pluginRow.getByRole('button', { name: '编辑', exact: true }).click();
  await page.locator('#plugin-modal').waitFor({ state: 'visible' });
  check(await page.locator('#plugin-modal-title').textContent() === '编辑设备端插件', 'editor title missing');
  check(await page.locator('#plugin-modal-action').inputValue() === 'set_light', 'plugin detail missing');
  await page.locator('#plugin-modal-name').fill('更新后的灯光控制');
  await page.locator('#plugin-modal-submit-btn').click();
  await page.waitForFunction(() => document.getElementById('toast').textContent === '插件已更新');
  check(writes.length === 1, 'edit did not issue one PUT');
  check(writes[0].input_params[0].enum.join(',') === 'day,night', 'edit lost enum');
  const missingRow = page.locator('#plugin-tools-list > div').filter({ hasText: '云端已删除插件' });
  await missingRow.getByRole('button', { name: '编辑', exact: true }).click();
  await page.waitForFunction(() => document.getElementById('toast').textContent === '设备插件不存在');
  check(await page.locator('#plugin-modal').isHidden(), 'missing plugin opened an empty create form');
  await page.evaluate(() => { window.confirm = () => true; });
  await missingRow.getByRole('button', { name: '删除', exact: true }).click();
  await page.waitForFunction(() => !document.getElementById('plugin-tools-list').textContent.includes('云端已删除插件'));
  await page.setViewportSize({ width: 360, height: 900 });
  await page.locator('[data-role-id="role-1"]').click();
  const edit = page.locator('#plugin-tools-list').getByRole('button', { name: '编辑', exact: true });
  await edit.scrollIntoViewIfNeeded();
  check(await edit.isVisible(), 'mobile edit action missing');
  const box = await edit.boundingBox();
  check(box.x >= 0 && box.x + box.width <= 360, 'mobile edit button overflows');
  // Switch through the actual role selector to exercise detail -> selection -> render.
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator('[data-role-id="default-role"]').click();
  await page.waitForFunction(() => document.getElementById('field-name').value === '默认角色');
  for (const width of [1440, 360]) {
    await page.setViewportSize({ width, height: 1000 });
    const linkedPlugins = page.locator('#plugin-tools-list');
    const linkedMcp = page.locator('#mcp-tools-list');
    check((await linkedPlugins.textContent()).includes('更新后的灯光控制'), 'default role lost known plugin name');
    check((await linkedPlugins.textContent()).includes('设备呼叫'), 'default role lost resolved plugin name');
    check(!(await linkedPlugins.textContent()).includes('cloud-plugin'), 'default role displays ID despite resolved name');
    check((await linkedMcp.textContent()).includes('天气查询'), 'default role lost known MCP name');
    check((await linkedMcp.textContent()).includes('cloud-mcp'), 'default role lost MCP absent from catalog');
    check(!(await linkedMcp.textContent()).includes('未关联工具'), 'default role shows unrelated MCP');
    check(await linkedPlugins.getByText('已关联', { exact: true }).count() === 2, 'default plugin associations incomplete');
    check(await linkedMcp.getByText('已关联', { exact: true }).count() === 2, 'default MCP associations incomplete');
    check(await linkedPlugins.locator('button').count() === 0 && await linkedMcp.locator('button').count() === 0, 'default role exposes resource modification');
    check(!await page.locator('#field-name').isEditable(), 'default role is editable');
    for (const list of [linkedPlugins, linkedMcp]) {
      const bounds = await list.boundingBox();
      check(bounds.x >= 0 && bounds.x + bounds.width <= width, 'default associations overflow screen');
    }
  }
  check(errors.length === 0, errors.join('\n'));
  return 'PASS: plugin edit, detail errors, stale deletion, default role MCP/plugin associations, read-only controls and desktop/mobile layout';
}
