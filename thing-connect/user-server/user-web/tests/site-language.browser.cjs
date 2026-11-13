const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs = require('fs'), path = require('path'), http = require('http'), assert = require('assert/strict');
const root = path.resolve(__dirname, '../../..');
const {execFileSync} = require('child_process');
const pages=JSON.parse(execFileSync(process.env.GO || 'go',['run','./user-server/user-web/tests/fixtures/public-pages'],{cwd:root,env:process.env,encoding:'utf8'}));
const han = /[\u3400-\u9fff]/;
const server = http.createServer((req, res) => {
 const url = new URL(req.url, 'http://localhost');
 if (url.pathname === '/v1/config/language.js') {
  res.setHeader('Content-Type', 'text/javascript');
  return res.end('window.WebLanguageConfig={default:"en",allow_switch:true};'+fs.readFileSync(root+'/internal/webui/runtime.js','utf8')+';WebI18n.addMessages('+fs.readFileSync(root+'/internal/webui/common.json','utf8')+');');
 }
 if(pages[url.pathname]) {res.setHeader('Content-Type','text/html');return res.end(pages[url.pathname]);}
 const file = url.pathname.startsWith('/static/') ? root+'/user-server'+url.pathname : url.pathname==='/' ? root+'/user-server/static/index.html' : url.pathname==='/boards' ? root+'/user-server/static/boards.html' : root+'/user-server/static/app/index.html';
 if (!fs.existsSync(file)) { res.writeHead(404); return res.end(); }
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.json')?'application/json':'text/html');res.end(fs.readFileSync(file));
});
(async () => {
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser = await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM || undefined,args:['--no-sandbox']});
 try {
  const context=await browser.newContext({locale:'zh-CN'}), page=await context.newPage(), errors=[];
  page.on('pageerror',error=>errors.push(error.message));
  await page.route('**/v1/**',async route=>{
   const u=new URL(route.request().url());if(u.pathname.endsWith('/language.js'))return route.continue();
   let data={};
   if(u.pathname==='/v1/user/device/list')data=[{device_id:'one',device_name:'Test device',status:1,online:true}];
   else if(u.pathname==='/v1/config/site')data={name:'测试站',name_en:'Test site'};
   else if(u.pathname==='/v1/user/me')data={email:'test@example.test'};
   else if(u.pathname==='/v1/boards')data={boards:[]};
   else if(u.pathname==='/v1/ai/roles/default')data={id:'preset',name:'Default role',agent_config:{prompt:'Example prompt'}};
   else if(u.pathname==='/v1/ai/roles')data={items:[{id:'custom',name:'Custom role',agent_config:{prompt:'Custom prompt'}}]};
   else if(u.pathname==='/v1/ai/roles/custom')data={id:'custom',name:'Custom role',agent_config:{prompt:'Custom prompt'}};
   else if(u.pathname.includes('/v1/ai/device/'))data={role_id:'custom',default_role_id:'preset'};
   else if(u.pathname.includes('/v1/ai/'))data={items:[]};
   else if(u.pathname.includes('/v1/call/group/web/device/'))data={desired_state:'left',state:'left',online:true};
   else if(u.pathname==='/v1/call/user/contacts')data={contacts:[]};
   else if(u.pathname==='/v1/call/user/contacts/pending')data={pending:[]};
   await route.fulfill({json:{code:200,data}});
  });
  async function english(label) {
   const leftovers=await page.evaluate(()=>{
    const han=/[\u3400-\u9fff]/, found=[];
    const walker=document.createTreeWalker(document.body,NodeFilter.SHOW_TEXT);
    while(walker.nextNode()) {const n=walker.currentNode,p=n.parentElement;if(p.closest('script,style,[data-language-toggle],[data-site-language-toggle],.ant-dropdown-hidden')||!p.getClientRects().length)continue; if(han.test(n.textContent))found.push(n.textContent.trim());}
    for(const e of document.querySelectorAll('input[placeholder],textarea[placeholder]'))if(e.getClientRects().length&&han.test(e.placeholder))found.push(e.placeholder);
    return [...new Set(found)];
   });
   assert.deepEqual(leftovers,[],label+' untranslated UI');
   assert.equal(await page.locator('html').getAttribute('lang'),'en',label);
  }
  await page.goto('http://127.0.0.1:'+server.address().port+'/login');
  const base='http://127.0.0.1:'+server.address().port;
  for(const url of ['/login','/register','/forgot-password','/devices','/bind','/contacts?device_id=one','/room?device_id=one','/agent?device_id=one','/player?device_id=one','/','/boards','/getting-started','/faq','/guides/simulator','/guides/wechat-voip','/guides/linux-device','/boards/demo','/static/legal/terms.html#service','/static/legal/privacy.html']) {
   await page.evaluate(()=>{localStorage.setItem('token','fixture-token');sessionStorage.clear();});
   await page.goto(base+url);await page.waitForTimeout(250);await english(url);
   if(url.includes('/legal/'))assert(page.url().includes('.en.html'),'legal page follows English preference');
   if(pages[url.split('#')[0]] && url!=='/' && url!=='/boards') {
    await page.evaluate(()=>WebI18n.setLanguage('zh-CN'));
    assert.equal(await page.locator('html').getAttribute('lang'),'zh-CN');
    await page.evaluate(()=>WebI18n.setLanguage('en'));await english(url+' after switching back');
    for(const width of [390,1440]) {
     await page.setViewportSize({width,height:900});
     assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),url+' English overflow at '+width);
     assert(await page.locator('[data-language-toggle]').isVisible(),url+' language switch at '+width);
    }
   }
   console.log('PASS English '+url);
  }
  await page.evaluate(()=>WebI18n.setLanguage('zh-CN'));
  await page.waitForURL('**/privacy.html');
  assert.equal(await page.locator('html').getAttribute('lang'),'zh-CN');
  assert(han.test(await page.locator('h1').innerText()));
  await page.evaluate(()=>WebI18n.setLanguage('en'));
  await page.waitForURL('**/privacy.en.html');
  await page.goto(base+'/agent?device_id=one');
  await page.getByRole('radio',{name:'Extensions',exact:true}).locator('..').click();
  await page.getByText('Tools and plugins',{exact:true}).click();
  await page.getByRole('button',{name:'+ New device plugin',exact:true}).click();
  await page.getByRole('button',{name:'Add parameter',exact:true}).first().click();
  await english('plugin parameters dialog');
  await page.getByRole('button',{name:'Remove parameter',exact:true}).first().click();
  await english('plugin parameter removed');
  assert.deepEqual(errors,[]);
 } finally {await browser.close();await new Promise(resolve=>server.close(resolve));}
})().catch(error=>{console.error(error);process.exitCode=1;server.close();});
