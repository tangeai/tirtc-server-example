const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs=require('fs'),path=require('path'),http=require('http'),assert=require('assert/strict');
const root=path.resolve(__dirname,'../../..');
fs.mkdirSync(path.join(root,'../output/playwright'),{recursive:true});
const requests=[],errors=[];
const server=http.createServer((req,res)=>{const url=new URL(req.url,'http://localhost');let file;
 if(url.pathname==='/v1/config/language.js'){res.setHeader('Content-Type','text/javascript');res.end('window.WebLanguageConfig={default:"zh-CN",allow_switch:true};'+fs.readFileSync(root+'/internal/webui/runtime.js','utf8')+';WebI18n.addMessages('+fs.readFileSync(root+'/internal/webui/common.json','utf8')+');');return;}
 file=url.pathname.startsWith('/static/')?root+'/user-server'+url.pathname:root+'/user-server/static/app/index.html';
 if(!fs.existsSync(file)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});

const results=[];
(async()=>{
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM||undefined,args:['--no-sandbox']});
 async function check(name,run){
  const context=await browser.newContext({viewport:{width:390,height:844}});const page=await context.newPage();page.setDefaultTimeout(4000);const pageErrors=[];page.on('pageerror',e=>pageErrors.push(e.message));
  await page.route('**/v1/**',async route=>{
   const p=new URL(route.request().url()).pathname;if(p.endsWith('/language.js'))return route.continue();let data={};
   if(p==='/v1/user/device/list')data=[{device_id:'one',device_name:'设备一',status:1}];
   else if(p==='/v1/user/me')data={email:'test@example.test'};
   else if(p==='/v1/config/captcha')data={enabled:false};
   else if(p==='/v1/ai/roles')data={items:[{id:'own',name:'自有角色',agent_config:{prompt:'test'}}]};
   else if(p==='/v1/ai/roles/own')data={id:'own',name:'自有角色',agent_config:{prompt:'test'}};
   else if(p==='/v1/ai/device/one/role')data={role_id:'own',default_role_id:''};
   else if(p==='/v1/ai/knowledge/indexes')data={items:[]};
   else if(p==='/v1/call/user/contacts')data={contacts:[]};
   else if(p==='/v1/call/user/contacts/pending')data={pending:[]};
   else if(p.startsWith('/v1/ai/'))data={items:[]};
   await route.fulfill({json:{code:200,data}});
  });
  try{await page.goto(base+'/login');await run(page,context);assert.deepEqual(pageErrors,[]);results.push({name,status:'pass'});console.log('PASS',name);}
  catch(e){results.push({name,status:'fail',error:e.message});console.error('FAIL',name,e.message);await page.screenshot({path:root+'/../output/playwright/audit-'+results.length+'.png'}).catch(()=>{});}
  finally{await context.close();}
 }
 async function signed(page,url){await page.evaluate(()=>localStorage.setItem('token','synthetic'));await page.goto(base+url);}
 await check('password visibility is independent, keyboard accessible and never submits',async page=>{
  let submissions=0;page.on('request',r=>{if(r.method()==='POST')submissions++;});
  for(const width of [320,390,1440]){
   await page.setViewportSize({width,height:900});
   for(const [url,labels] of [['/login',['密码']],['/register',['设置密码','确认密码']],['/forgot-password',['新密码','确认新密码']]]){
    await page.goto(base+url);
    for(const label of labels){
     const input=page.getByLabel(label,{exact:true});await input.fill('synthetic-password');assert.equal(await input.getAttribute('type'),'password');
     const toggle=page.getByRole('button',{name:'显示'+label,exact:true});await toggle.click();assert.equal(await input.getAttribute('type'),'text');assert.equal(await input.inputValue(),'synthetic-password');
     const hide=page.getByRole('button',{name:'隐藏'+label,exact:true});await hide.focus();await page.keyboard.press('Space');assert.equal(await input.getAttribute('type'),'password');
     const box=await toggle.boundingBox();assert(box.width>=44&&box.height>=44);assert(box.x>=0&&box.x+box.width<=width);
    }
    if(labels.length===2){await page.getByRole('button',{name:'显示'+labels[0],exact:true}).click();assert.equal(await page.getByLabel(labels[1],{exact:true}).getAttribute('type'),'password');}
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
    if(width===390&&url==='/register')await page.screenshot({path:root+'/../output/playwright/password-toggle-register-390.png',fullPage:true});
   }
  }
  assert.equal(submissions,0);
 });
 await check('HTTP 401 with HTML clears session and returns to login',async page=>{
  await page.route('**/v1/user/device/list',route=>route.fulfill({status:401,contentType:'text/html',body:'<h1>Unauthorized</h1>'}));
  await signed(page,'/devices');await page.waitForURL('**/login');assert.equal(await page.evaluate(()=>localStorage.getItem('token')),null);await page.getByRole('alert').filter({hasText:'登录状态已失效'}).waitFor();
 });
 await check('HTML 502 keeps session and supports retry',async page=>{
  let fail=true;await page.route('**/v1/user/device/list',route=>fail?route.fulfill({status:502,contentType:'text/html',body:'<h1>Bad Gateway</h1>'}):route.fallback());
  await signed(page,'/devices');await page.getByRole('alert').waitFor();assert.equal(new URL(page.url()).pathname,'/devices');assert.equal(await page.evaluate(()=>localStorage.getItem('token')),'synthetic');fail=false;await page.getByRole('button',{name:'刷新',exact:true}).click();await page.getByRole('heading',{name:'设备一',exact:true}).waitFor();
 });
 await check('register and reset accept six digits pasted with spaces',async page=>{
  for(const route of ['/register','/forgot-password']){await page.goto(base+route);const input=page.getByLabel('邮箱验证码',{exact:true});await input.fill('12 34 56');assert.equal(await input.inputValue(),'123456');}
 });
 await check('knowledge dirty Escape retains draft until confirmed',async page=>{
  await signed(page,'/agent?device_id=one');await page.getByRole('button',{name:'扩展能力',exact:true}).click();await page.locator('summary').filter({hasText:'知识库'}).click();await page.getByRole('button',{name:'创建知识库',exact:true}).click();
  await page.getByLabel('知识库名称',{exact:true}).fill('未保存草稿');await page.keyboard.press('Escape');const confirm=page.getByRole('dialog',{name:'修改尚未保存',exact:true});await confirm.waitFor();await confirm.getByRole('button',{name:'继续编辑',exact:true}).click();assert.equal(await page.getByLabel('知识库名称',{exact:true}).inputValue(),'未保存草稿');
  await page.keyboard.press('Escape');await confirm.getByRole('button',{name:'放弃修改',exact:true}).click();assert.equal(await page.getByRole('dialog').count(),0);
 });
 await check('knowledge slow save cannot be dismissed or submitted twice',async page=>{
  let release,started,posts=0;const blocked=new Promise(r=>release=r),seen=new Promise(r=>started=r);
  await page.route('**/v1/ai/knowledge/indexes',async route=>{if(route.request().method()!=='POST')return route.fallback();posts++;started();await blocked;await route.fulfill({json:{code:200,data:{index_id:'saved'}}});});
  await signed(page,'/agent?device_id=one');await page.getByRole('button',{name:'扩展能力',exact:true}).click();await page.locator('summary').filter({hasText:'知识库'}).click();await page.getByRole('button',{name:'创建知识库',exact:true}).click();
  await page.getByLabel('知识库名称',{exact:true}).fill('知识库');await page.getByLabel('知识库描述',{exact:true}).fill('描述');const dialog=page.getByRole('dialog');await dialog.getByRole('button',{name:'保存',exact:true}).click();await seen;
  try{assert(await dialog.getByRole('button',{name:'关闭弹窗',exact:true}).isDisabled());await page.keyboard.press('Escape');assert(await dialog.isVisible());assert.equal(posts,1);}finally{release();}
  await dialog.waitFor({state:'hidden'});
 });
 await check('contact request accepted immediately reports established friendship',async page=>{
  await page.route('**/v1/call/user/contacts/request',route=>route.fulfill({json:{code:200,data:{status:'accepted'}}}));
  await signed(page,'/contacts?device_id=one');await page.getByRole('button',{name:'添加联系人',exact:true}).click();await page.getByLabel('对方设备 ID',{exact:true}).fill('peer');await page.getByRole('button',{name:'发起申请',exact:true}).click();await page.getByRole('dialog').getByRole('status').filter({hasText:'已成为联系人'}).waitFor();
 });
 await browser.close();server.close();fs.writeFileSync(root+'/../output/playwright/cross-page-edge-results.json',JSON.stringify(results,null,2));if(results.some(r=>r.status==='fail'))process.exitCode=1;
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
