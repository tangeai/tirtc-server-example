// Build admin-web first. Reuse the repository's user-web Playwright installation.
const { chromium } = require('../../../user-server/user-web/node_modules/playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const dist = path.resolve(__dirname, '../dist');
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const file = url.pathname.startsWith('/admin/assets/')
    ? path.join(dist, url.pathname.slice('/admin/'.length)) : path.join(dist, 'index.html');
  if (!fs.existsSync(file)) { res.writeHead(404).end(); return; }
  res.setHeader('Content-Type', file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html');
  res.end(fs.readFileSync(file));
});
(async () => {
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const browser = await chromium.launch({headless:true, executablePath:process.env.PLAYWRIGHT_CHROMIUM, args:['--no-sandbox']});
  try {
    const page = await browser.newPage();
    const errors = [], mutations = [];
    let unavailable = false;
    let item = {id:'test',name:'后台测试类型',language:'zh',voice_id:'shared',voice_model:'cosyvoice-v3-flash',prompt:'原始设定',status:'published',revision:1,sort_order:0};
    page.on('pageerror', error => errors.push(error.message));
    await page.route('**/v1/**', async route => {
      const req = route.request(), pathname = new URL(req.url()).pathname;
      let data = {};
      if (pathname === '/v1/setup/status') return route.fulfill({status:404});
      if (pathname.endsWith('/auth/refresh')) data = {access_token:'synthetic-token'};
      else if (pathname.endsWith('/me/navigation')) data = {menus:[{id:1,parent_id:0,menu_code:'ai',name:'AI服务',path:'/configs/ai-server',icon:'',menu_type:2,sort_no:0}]};
      else if (pathname.endsWith('/me')) data = {user:{id:1,email:'test@example.test',nick_name:'测试管理员',must_change_password:0}};
      else if (pathname.endsWith('/ai-role-types/voices')) {
        if (unavailable) return route.fulfill({status:503,json:{code:50300,msg:'音色列表暂不可用'}});
        data = {items:[{id:'shared',name:'Flash声音',model:'cosyvoice-v3-flash',sample_url:'/flash.wav'},{id:'shared',name:'V3声音',model:'cosyvoice-v3'}]};
      } else if (pathname.endsWith('/ai-role-types/test') && req.method() === 'PUT') {
        const body = req.postDataJSON(); mutations.push(body);
        item = {...body.role_type,revision:item.revision+1}; data = item;
      } else if (pathname.endsWith('/ai-role-types')) data = {items:[item]};
      else data = {items:[],revisions:[],definitions:[]};
      await route.fulfill({json:{code:200,msg:'ok',data}});
    });
    const open = async () => {
      await page.goto(`http://127.0.0.1:${server.address().port}/admin/configs/ai-server`);
      await page.getByRole('tab',{name:'角色类型',exact:true}).click();
      await page.getByRole('button',{name:/编\s*辑/,exact:true}).click();
    };
    const dialog = page.getByRole('dialog',{name:'编辑角色类型'});
    const save = async () => {
      await dialog.getByLabel('操作说明',{exact:true}).fill('自动化测试');
      await dialog.getByRole('button',{name:'保存类型',exact:true}).click();
      await dialog.waitFor({state:'hidden'});
    };
    await open();
    assert.equal(await dialog.getByRole('button',{name:'试听所选音色'}).isEnabled(),true);
    await dialog.locator('.ant-select-selector').click();
    await page.getByText('V3声音 · cosyvoice-v3',{exact:true}).click();
    assert.equal(await dialog.getByRole('button',{name:'试听所选音色'}).isDisabled(),true);
    await save();
    assert.equal(item.voice_id,'shared'); assert.equal(item.voice_model,'cosyvoice-v3');
    assert.equal(item.prompt,'原始设定'); assert.equal(mutations[0].expected_revision,1);
    assert.equal('voice_selection' in mutations[0].role_type,false);
    assert.equal('reason' in mutations[0].role_type,false);
    await open();
    await dialog.getByText('V3声音 · cosyvoice-v3',{exact:true}).waitFor();
    await dialog.locator('.ant-select-selector').click();
    await page.getByText('Flash声音 · cosyvoice-v3-flash',{exact:true}).click();
    assert.equal(await dialog.getByRole('button',{name:'试听所选音色'}).isEnabled(),true);
    await save();
    assert.equal(item.voice_model,'cosyvoice-v3-flash');
    unavailable = true;
    await open();
    await dialog.getByText('音色列表暂不可用，已有音色和提示词仍可编辑保存。').waitFor();
    await dialog.getByLabel('角色类型名称',{exact:true}).fill('目录故障时改名');
    await save();
    assert.equal(item.name,'目录故障时改名');
    assert.equal(item.voice_id,'shared'); assert.equal(item.voice_model,'cosyvoice-v3-flash');
    assert.equal(item.prompt,'原始设定'); assert.equal(mutations.length,3);
    assert.deepEqual(errors,[]);
    console.log('PASS admin exact model/voice selection, preview isolation, save/reopen and catalog outage preservation');
  } finally { await browser.close(); server.close(); }
})().catch(error => { console.error(error); server.close(); process.exitCode=1; });
