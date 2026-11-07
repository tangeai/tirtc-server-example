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
 else if(u.pathname==='/v1/user/me')data={email:'test@example.test'};
 else if(u.pathname==='/v1/user/device/name'){if(renameWait)await renameWait;}
 else if(u.pathname==='/v1/ai/roles')data={items:[role,...Array.from({length:4},(_,i)=>({...role,id:'extra'+i,name:'角色示例 '+(i+1),user_params:{description:'角色的简短描述，便于快速区分用途'}}))]};
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
 for(const width of [320,390,768,1440]){
  await page.setViewportSize({width,height:900});
  await page.route('**/v1/user/device/rtc-token?*',route=>route.fulfill({status:503,json:{code:50000,msg:'暂时无法连接，请确认设备在线后重试'}}));
  await page.goto(base+'/player?device_id=one');await page.getByRole('button',{name:'重新连接'}).waitFor();
  assert.equal(await page.locator('.player-card .player-connection button').count(),1);
  const card=await page.locator('.player-card').boundingBox();assert(card.y + card.height <= 900,'player fits viewport');
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:root+'/../output/playwright/player-compact-'+width+'.png',fullPage:true});
  await page.goto(base+'/agent?device_id=one');await page.locator('.role-card').first().waitFor({state:'attached'});if(width<=640)await page.getByRole('button',{name:'返回角色列表'}).click();
  if(width>640){const sidebar=await page.locator('.role-list').boundingBox();assert(sidebar.width<=260);}
  const cards=await page.locator('.role-card').all();assert.equal(cards.length,6);const boxes=await Promise.all(cards.map(card=>card.boundingBox()));assert(boxes.every(box=>box.height<=110),'compact role cards');if(width>640)assert(boxes.filter(box=>box.y+box.height<=900).length>=5,'at least five roles visible');
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:root+'/../output/playwright/roles-compact-'+width+'.png',fullPage:true});
 }
 assert.deepEqual(errors,[]);console.log('PASS: compact player and role sidebar at 4 viewport widths.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
