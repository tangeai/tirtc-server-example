const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs=require('fs'),path=require('path'),http=require('http'),assert=require('assert/strict');
const root=path.resolve(__dirname,'../../..');
fs.mkdirSync(path.join(root,'../output/playwright'),{recursive:true});
const requests=[],errors=[];
const server=http.createServer((req,res)=>{const url=new URL(req.url,'http://localhost');let file;
 if(url.pathname==='/v1/config/language.js'){res.setHeader('Content-Type','text/javascript');res.end('window.WebLanguageConfig={default:"zh-CN",allow_switch:true};'+fs.readFileSync(root+'/internal/webui/runtime.js','utf8')+';WebI18n.addMessages('+fs.readFileSync(root+'/internal/webui/common.json','utf8')+');');return;}
 file=url.pathname.startsWith('/static/')?root+'/user-server'+url.pathname:url.pathname==='/'?root+'/user-server/static/index.html':url.pathname==='/boards'?root+'/user-server/static/boards.html':root+'/user-server/static/app/index.html';
 if(!fs.existsSync(file)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM || undefined,args:['--no-sandbox']});
 try{
 const context=await browser.newContext();const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));

 let bound='preset', role={id:'custom',name:'我的角色',agent_config:{prompt:'设定',mcp_tools:['missing'],device_plugins:[]},service_config:{type:'external',config:{opaque:'keep'}},user_params:{description:'测试描述'}};
 let renameRelease,renameWait,emptyMode=false,errorMode=false;
 const scheme={scheme_id:'sound',scheme_version:1,name:'环境声音识别',description:'识别环境声音并返回文字说明。',output_mode:'text',limits:{max_audio_seconds:30}};
 await page.route('**/v1/**',async route=>{const req=route.request(),u=new URL(req.url());if(u.pathname.endsWith('/language.js'))return route.continue();requests.push({path:u.pathname,body:req.postDataJSON(),method:req.method()});let data={};
 if(errorMode && u.pathname==='/v1/user/device/list')return route.fulfill({status:503,json:{code:50000,msg:'设备列表暂不可用，请重试'}});
 if(emptyMode && u.pathname==='/v1/user/device/list')return route.fulfill({json:{code:200,data:[]}});
 if(u.pathname==='/v1/user/device/list')data=[{device_id:'one',device_name:'设备一',status:1,online:true},{device_id:'two',device_name:'设备二',status:1}];
 else if(u.pathname==='/v1/boards')data={boards:[{name:'小钛开发板',model:'ESP32-S3-DISPLAY-BOARD',vendor:'开发者示例',chip:'ESP32-S3',adaptation_status:'ready',summary:'支持语音对话、实时查看和设备通信。',detail_slug:'demo',capabilities:['AI','对讲'],repository_url:'https://example.test/source',flashing_guide_url:'https://example.test/guide'}]};
 else if(u.pathname==='/v1/user/me')data={email:'test@example.test'};
 else if(u.pathname==='/v1/user/device/name'){if(renameWait)await renameWait;}
 else if(u.pathname==='/v1/ai/roles')data={items:[role]};
 else if(u.pathname==='/v1/ai/roles/default')data={id:'preset',name:'预设角色',agent_config:{prompt:'只读'}};
 else if(u.pathname==='/v1/ai/roles/custom'){if(req.method()==='PUT')role={...role,...req.postDataJSON()};if(req.method()==='DELETE')return route.fulfill({status:409,json:{code:40900,msg:'角色正在使用，请先解除设备关联',data:{devices:[{device_id:'two',device_name:'设备二'}]}}});data=role;}
 else if(u.pathname.includes('/v1/ai/device/')){if(req.method()==='PUT')bound=req.postDataJSON().role_id;data={role_id:bound==='preset'?'':bound,default_role_id:'preset'};}
 else if(u.pathname.includes('/v1/recognition/devices/'))data={policy:{enabled:true,revision:1,monthly_limit:100},schemes:[scheme],usage:{used:5}};
 else if(u.pathname==='/v1/recognition/types')data=[scheme];
 else if(u.pathname.includes('/v1/call/group/web/device/'))data={desired_state:'left',state:'left',online:true};
 else if(u.pathname==='/v1/ai/voices')data={items:[]};
 else if(u.pathname.includes('/v1/ai/'))data={items:[]};
 else if(u.pathname==='/v1/call/user/contacts')data={contacts:[{id:1,type:'device',source:'manual',device_id:'peer',device_name:'客厅设备',online:true},{id:2,type:'device',source:'auto',device_id:'family',device_name:'家人设备',online:false}]};
 else if(u.pathname==='/v1/call/user/contacts/pending')data={pending:[]};
 await route.fulfill({json:{code:200,data}});
 });

 await page.goto(base+'/login');await page.evaluate(()=>localStorage.setItem('token','test'));
 const measurements=[];
 for(const width of [320,390,768,1440,1920]){
  await page.setViewportSize({width,height:900});
  for(const [name,url] of Object.entries({login:'/login',register:'/register',reset:'/forgot-password',devices:'/devices',bind:'/bind',contacts:'/contacts?device_id=one',room:'/room?device_id=one',roles:'/agent?device_id=one',player:'/player?device_id=one',home:'/',boards:'/boards',terms:'/static/legal/terms.html',privacy:'/static/legal/privacy.html'})){
   await page.evaluate(({name})=>{sessionStorage.clear();if(['login','register','reset','home','boards','terms','privacy'].includes(name))localStorage.removeItem('token');else localStorage.setItem('token','test');},{name});
   await page.goto(base+url);await page.waitForTimeout(300);
   if(name==='home'){await page.evaluate(async()=>{for(let y=0;y<document.body.scrollHeight;y+=600){scrollTo(0,y);await new Promise(r=>setTimeout(r,60));}});await page.waitForTimeout(400);}
   await page.evaluate(()=>scrollTo(0,0));
   await page.screenshot({path:root+'/../output/playwright/layout-'+(process.env.STAGE||'before')+'-'+name+'-'+width+'.png',fullPage:true});
   measurements.push(await page.evaluate(({name,width})=>({name,width,overflow:document.documentElement.scrollWidth>innerWidth,body:getComputedStyle(document.body).fontSize,heading:[...document.querySelectorAll('h1')].map(e=>getComputedStyle(e).fontSize),buttons:[...new Set([...document.querySelectorAll('button')].filter(e=>e.getBoundingClientRect().height>0).map(e=>getComputedStyle(e).fontSize+'/'+Math.round(e.getBoundingClientRect().height)))],inputs:[...new Set([...document.querySelectorAll('input:not([type=checkbox]):not([type=radio]),select')].filter(e=>e.getBoundingClientRect().height>0).map(e=>getComputedStyle(e).fontSize+'/'+Math.round(e.getBoundingClientRect().height)))]}),{name,width}));

   if(!['home','boards','terms','privacy'].includes(name)){
    const control=page.getByRole('button',{name:'语言 / Language',exact:true});await control.click();
    const menu=page.locator('.language-panel');await menu.waitFor();const box=await menu.boundingBox();assert(box.x>=0&&box.x+box.width<=width);
    await menu.getByRole('button',{name:'English',exact:true}).click();assert(await control.locator('svg').isVisible(),'language icon');assert.equal(await page.locator('.language-panel').count(),0);
    if(!await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)){await page.screenshot({path:root+'/../output/playwright/overflow-'+name+'-'+width+'.png',fullPage:true});console.log(await page.locator('*').evaluateAll(nodes=>nodes.filter(e=>e.getBoundingClientRect().right>innerWidth+1).map(e=>({tag:e.tagName,cls:e.className,text:e.textContent.slice(0,70),right:e.getBoundingClientRect().right})).slice(-20)));throw Error('English overflow '+name+' '+width);}
    await page.screenshot({path:root+'/../output/playwright/layout-'+(process.env.STAGE||'before')+'-'+name+'-en-'+width+'.png',fullPage:true});
    await control.click();await page.keyboard.press('Escape');assert.equal(await page.locator('.language-panel').count(),0);assert(await control.evaluate(e=>e===document.activeElement));
    await control.click();await page.locator('.language-panel').getByRole('button',{name:'简体中文',exact:true}).click();
   }
  }
  async function captureDialog(name){
   const dialog=page.getByRole('dialog');await dialog.waitFor();await page.waitForTimeout(100);
   const box=await dialog.boundingBox();assert(box.x>=0&&box.y>=0&&box.x+box.width<=width&&box.y+box.height<=901,name+' outside viewport');
   assert(await dialog.evaluate(e=>e.scrollWidth<=e.clientWidth),name+' internal overflow');
   const close=dialog.locator('.close');if(await close.count()){assert.equal(await close.evaluate(e=>getComputedStyle(e).fontSize),'26px',name+' close icon size');await dialog.evaluate(e=>e.scrollTop=e.scrollHeight);const cb=await close.boundingBox();assert(cb.y>=box.y&&cb.y+cb.height<=box.y+box.height,name+' close stays visible when scrolling');await dialog.evaluate(e=>e.scrollTop=0);}
   await page.screenshot({path:root+'/../output/playwright/state-'+name+'-'+width+'.png'});await page.keyboard.press('Escape');
   const discard=page.getByRole('dialog',{name:'修改尚未保存',exact:true});if(await discard.isVisible())await discard.getByRole('button',{name:'放弃修改',exact:true}).click();
  }
  await page.goto(base+'/boards');await page.locator('.featured-card').first().click();await captureDialog('开发板详情');
  await page.evaluate(()=>{localStorage.setItem('token','test');sessionStorage.clear();});await page.goto(base+'/devices');await page.getByText('设备一',{exact:true}).waitFor();
  const first=page.locator('[data-device-id="one"]');
  for(const label of ['设备信息','修改名称','解绑设备']){await first.getByText('更多 ▾').click();await first.getByRole('button',{name:label,exact:true}).click();await captureDialog(label);}
  await first.getByRole('button',{name:/AI识别/}).click();await page.getByText('环境声音识别',{exact:false}).waitFor();await captureDialog('识别配置');
  await page.goto(base+'/contacts?device_id=one');await page.getByRole('button',{name:'添加联系人',exact:true}).click();await captureDialog('添加联系人');
  await page.getByRole('button',{name:'改备注',exact:true}).first().click();await page.getByLabel('联系人备注').fill('长名称测试-'.repeat(8));assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.getByRole('button',{name:'取消',exact:true}).click();
  await page.getByRole('button',{name:'删除',exact:true}).first().click();await captureDialog('删除联系人');
  await page.goto(base+'/agent?device_id=one');await page.getByLabel('智能体名称',{exact:true}).waitFor();
  if(width<=640)await page.getByRole('button',{name:'返回角色列表',exact:false}).click();
  await page.getByRole('button',{name:'我的角色',exact:true}).click();await page.getByLabel('智能体名称',{exact:true}).waitFor();
  await page.getByRole('button',{name:'扩展能力',exact:true}).click();
  await page.getByText('知识库',{exact:true}).filter({hasNot:page.locator('select')}).first().click();
  await page.getByRole('button',{name:'创建知识库',exact:true}).click();await captureDialog('创建知识库');
  await page.getByText('工具与插件',{exact:true}).click();
  await page.getByRole('button',{name:'+ 新建 MCP 工具',exact:true}).click();await captureDialog('新建MCP');
  await page.getByRole('button',{name:'+ 新建设备插件',exact:true}).click();await page.getByRole('button',{name:'添加参数',exact:true}).first().click();await captureDialog('新建插件参数');
  await page.getByRole('button',{name:'角色',exact:true}).click();
  await page.getByLabel('智能体名称',{exact:true}).fill('这是一个比较长的角色名称-'.repeat(3));
  await page.screenshot({path:root+'/../output/playwright/state-role-dirty-'+width+'.png',fullPage:true});
  // Avoid the intentional unsaved-changes navigation prompt in the next viewport.
  await page.getByRole('button',{name:'放弃修改',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'放弃修改',exact:true}).click();
  for(const state of ['empty','error']){
   emptyMode=state==='empty';errorMode=state==='error';await page.evaluate(()=>sessionStorage.clear());
   for(const url of ['/devices','/contacts']){await page.goto(base+url);await page.waitForTimeout(100);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:root+'/../output/playwright/state-'+state+'-'+url.slice(1)+'-'+width+'.png',fullPage:true});}
  }
  emptyMode=false;errorMode=false;

 }
 fs.writeFileSync(root+'/../output/playwright/layout-'+(process.env.STAGE||'before')+'-metrics.json',JSON.stringify(measurements,null,2));
 console.log('Measured page/viewport combinations:',measurements.length);
 assert.deepEqual(errors,[]);assert.deepEqual(measurements.filter(m=>m.overflow),[],'horizontal overflow');console.log('PASS: 13 pages at 5 viewport widths.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
