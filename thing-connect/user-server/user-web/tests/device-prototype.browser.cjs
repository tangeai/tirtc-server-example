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
 if(u.pathname==='/v1/user/device/list')data=[{device_id:'one',device_name:'设备一',status:1,online:true},{device_id:'two',device_name:'设备二',status:1},{device_id:'TG3T83FE246J',device_name:'未命名设备',status:1}];
 else if(u.pathname==='/v1/boards')data={boards:[{name:'小钛开发板',model:'ESP32-S3-DISPLAY-BOARD',vendor:'开发者示例',chip:'ESP32-S3',adaptation_status:'ready',summary:'支持语音对话、实时查看和设备通信。',detail_slug:'demo',capabilities:['AI','对讲'],repository_url:'https://example.test/source',flashing_guide_url:'https://example.test/guide'}]};
 else if(u.pathname==='/v1/config/navigation')data={links:[{name:'开发文档',url:'https://example.test/docs',enabled:true}]};
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
 for(const width of [320,390,768,1440,1920]){
  await page.setViewportSize({width,height:900});await page.evaluate(()=>{sessionStorage.clear();window.WebI18n.setLanguage('zh-CN');});await page.goto(base+'/devices');await page.getByText('预设角色（默认）',{exact:true}).first().waitFor();
  for(const language of ['zh-CN','en']){
   await page.evaluate(value=>window.WebI18n.setLanguage(value),language);
   const triggers=await page.locator('.header-actions .menu-trigger,.header-actions .language-trigger').all();assert.equal(triggers.length,3);
   const sizes=await Promise.all(triggers.map(item=>item.boundingBox()));assert(sizes.every(box=>box.height===44 && Math.abs(box.y-sizes[0].y)<1),'uniform header controls');
   await page.getByLabel(language==='en'?'Account menu':'账号菜单',{exact:true}).click();await page.getByRole('button',{name:language==='en'?'Log out':'退出登录',exact:true}).waitFor();await page.keyboard.press('Escape');
   const cards=page.locator('.device-card');
   for(const card of await cards.all()){
    const tiles=await card.locator('.device-summaries>*').all();assert.equal(tiles.length,3);
    const boxes=await Promise.all(tiles.map(tile=>tile.boundingBox()));assert(boxes.every(box=>Math.abs(box.y-boxes[0].y)<1),'three summary tiles must share one row');
    assert.equal(await card.locator('.device-actions>:is(a,button)').count(),4);assert.equal(await card.locator('.device-actions svg').count(),4);assert.equal(await card.locator('.device-symbol svg').count(),1);
   }
   assert.equal(await page.locator('.add-device').evaluate(e=>getComputedStyle(e).position),'static');
   const grid=await page.locator('.device-grid').boundingBox(),add=await page.locator('.add-device').boundingBox();assert(add.y>=grid.y+grid.height,'add button follows list');
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'no horizontal overflow');
   await page.locator('h1').click();
   await page.screenshot({path:root+'/../output/playwright/device-prototype-'+language+'-'+width+'.png',fullPage:true});
  }
 }
 assert.deepEqual(errors,[]);console.log('PASS: prototype geometry, icons, footer action and responsive Chinese/English at 5 widths.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
