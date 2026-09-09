// Run with playwright-cli run-code against the local development gateway.
async (page) => {
  await page.unrouteAll();
  await page.addInitScript(() => {
    localStorage.setItem('token', 'browser-test-token');
    const start = window.setInterval;
    window.testIntervals = 0;
    window.setInterval = (...args) => {window.testIntervals++; return start(...args);};
  });
  let online = true;
  let failure = false;
  let defaults = 0;
  await page.route('**/v1/user/me', route => route.fulfill({json:{code:200,data:{user_id:1,email:'browser@example.com'}}}));
  await page.route('**/v1/user/device/list', r => r.fulfill({json: {code:200,data:[
    {device_id:'state-1',online:true}, {device_id:'state-2',online:true}
  ]}}));
  await page.route('**/v1/ai/device/**/role', r => r.fulfill({json:{code:200,data:{role_id:'',default_role_id:'default-1'}}}));
  await page.route('**/v1/ai/roles/default', r => {defaults++;return r.fulfill({json:{code:200,data:{name:'默认助手'}}});});
  await page.route('**/v1/call/room/web/device/**', r => failure ? r.abort('failed') : r.fulfill({json:{code:200,data:{desired_state:'left',online}}}));
  await page.goto('http://dev-demo-open.tangeai.cn:8080/devices');
  await page.waitForFunction(() => [...document.querySelectorAll('[data-role]')].length === 2 && [...document.querySelectorAll('[data-role]')].every(e=>e.textContent==='默认助手（默认）'));
  if (defaults !== 1) throw Error('default role request not shared');
  await page.locator('[aria-haspopup=menu]').first().click();
  online = false;
  await page.evaluate(() => refreshSummaries());
  for (const width of [360,1440]) {
    await page.setViewportSize({width,height:900});
    const states = await page.locator('[data-online-status]').allTextContents();
    if (states.some(state=>state!=='离线')) throw Error('stale online state');
    const gray = await page.locator('[data-device-icon]').evaluateAll(icons=>icons.every(icon=>getComputedStyle(icon).color==='rgb(156, 163, 175)'));
    if (!gray) throw Error('icon did not turn gray');
    if (!await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)) throw Error('horizontal overflow');
  }
  await page.locator('[aria-haspopup=menu]').first().click();
  online = true;
  await page.evaluate(() => refreshSummaries());
  if (!await page.locator('#device-menu').isVisible()) throw Error('refresh closed active menu');
  if ((await page.locator('[data-online-status]').allTextContents()).some(s=>s!=='在线')) throw Error('reconnect not displayed');
  failure = true;
  await page.evaluate(() => refreshSummaries());
  if (!(await page.locator('[data-room]').first().textContent()).includes('状态获取失败')) throw Error('failure hidden');
  failure = false;
  await page.evaluate(() => {
    dispatchEvent(new PageTransitionEvent('pagehide', {persisted:true}));
    dispatchEvent(new PageTransitionEvent('pageshow', {persisted:true}));
  });
  await page.waitForFunction(()=>window.testIntervals===0);
  await page.getByRole('button', {name:'刷新', exact:true}).click();
  await page.waitForFunction(()=>!document.querySelector('[data-room]').textContent.includes('失败'));
  console.log('PASS: default role deduplication, offline/reconnect updates, open menu preservation, failure feedback, no polling and manual list refresh and responsive layout');
}
