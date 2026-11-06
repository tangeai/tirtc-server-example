const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs=require('fs'),path=require('path'),http=require('http'),assert=require('assert/strict');
const root=path.resolve(__dirname,'../../..');
fs.mkdirSync(path.join(root,'../output/playwright'),{recursive:true});
const requests=[],errors=[];
const server=http.createServer((req,res)=>{const url=new URL(req.url,'http://localhost');let file;
 if(url.pathname==='/v1/config/language.js'){res.setHeader('Content-Type','text/javascript');res.end('window.WebLanguageConfig={default:"zh-CN",allow_switch:true};'+fs.readFileSync(root+'/internal/webui/runtime.js','utf8')+';WebI18n.addMessages('+fs.readFileSync(root+'/internal/webui/common.json','utf8')+');');return;}
 file=url.pathname.startsWith('/static/')?root+'/user-server'+url.pathname:root+'/user-server/static/app/index.html';
 if(!fs.existsSync(file)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM || undefined,args:['--no-sandbox']});
 try{
 const context=await browser.newContext();const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));

 let bound='preset', role={id:'custom',name:'我的角色',agent_config:{prompt:'设定',mcp_tools:['missing'],device_plugins:[]},service_config:{type:'external',config:{opaque:'keep'}},user_params:{description:'测试描述'}};
 let renameRelease,renameWait;
 await page.route('**/v1/**',async route=>{const req=route.request(),u=new URL(req.url());if(u.pathname.endsWith('/language.js'))return route.continue();requests.push({path:u.pathname,body:req.postDataJSON(),method:req.method()});let data={};
 if(u.pathname==='/v1/user/device/list')data=[{device_id:'one',device_name:'设备一',status:1,online:true},{device_id:'two',device_name:'设备二',status:1}];
 else if(u.pathname==='/v1/user/me')data={email:'test@example.test'};
 else if(u.pathname==='/v1/user/device/name'){if(renameWait)await renameWait;}
 else if(u.pathname==='/v1/ai/roles')data={items:[role]};
 else if(u.pathname==='/v1/ai/roles/default')data={id:'preset',name:'预设角色',agent_config:{prompt:'只读'}};
 else if(u.pathname==='/v1/ai/roles/custom'){if(req.method()==='PUT')role={...role,...req.postDataJSON()};if(req.method()==='DELETE')return route.fulfill({status:409,json:{code:40900,msg:'角色正在使用，请先解除设备关联',data:{devices:[{device_id:'two',device_name:'设备二'}]}}});data=role;}
 else if(u.pathname.includes('/v1/ai/device/')){if(req.method()==='PUT')bound=req.postDataJSON().role_id;data={role_id:bound==='preset'?'':bound,default_role_id:'preset'};}
 else if(u.pathname.includes('/v1/recognition/devices/'))data={policy:{enabled:false},schemes:[]};
 else if(u.pathname.includes('/v1/call/group/web/device/'))data={desired_state:'left',state:'left',online:true};
 else if(u.pathname==='/v1/ai/voices')data={items:[]};
 else if(u.pathname.includes('/v1/ai/'))data={items:[]};
 await route.fulfill({json:{code:200,data}});
 });
 await page.goto(base+'/login');await page.evaluate(()=>localStorage.setItem('token','test'));
 let fresh,failSave=false;
 await page.route('**/v1/ai/roles',async route=>{if(route.request().method()==='POST'){fresh={...route.request().postDataJSON(),id:'fresh'};return route.fulfill({json:{code:200,data:fresh}});}return route.fallback();});
 await page.route('**/v1/ai/roles/fresh',async route=>{const req=route.request();if(req.method()==='DELETE')fresh=undefined;else if(req.method()==='PUT'){if(failSave){failSave=false;return route.fulfill({status:503,json:{code:50000,msg:'保存失败，请重试'}});}fresh={...fresh,...req.postDataJSON()};}await route.fulfill({json:{code:200,data:fresh||{}}});});
 await page.setViewportSize({width:1440,height:900});await page.goto(base+'/agent?device_id=one');
 await page.getByRole('button',{name:'新建角色',exact:false}).click();await page.getByLabel('智能体名称',{exact:true}).fill('新角色');await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.getByRole('heading',{name:'正在查看：新角色',exact:true}).waitFor();assert.equal(fresh.name,'新角色');
 await page.getByLabel('智能体名称',{exact:true}).fill('未保存');await page.getByRole('button',{name:'预设角色',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'继续编辑',exact:true}).click();assert.equal(await page.getByLabel('智能体名称',{exact:true}).inputValue(),'未保存');
 await page.getByRole('button',{name:'放弃修改',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'放弃修改',exact:true}).click();assert.equal(await page.getByLabel('智能体名称',{exact:true}).inputValue(),'新角色');
 await page.getByLabel('智能体名称',{exact:true}).fill('改名后');failSave=true;
 const switchFresh=()=>page.locator('.role-card').filter({has:page.getByRole('button',{name:'新角色',exact:true})}).getByRole('button',{name:'切换到此角色',exact:true});
 await switchFresh().click();await page.getByRole('dialog').getByRole('button',{name:'保存并切换',exact:true}).click();await page.getByRole('alert').waitFor();assert.equal(bound,'preset');assert.equal(await page.getByLabel('智能体名称',{exact:true}).inputValue(),'改名后');
 await switchFresh().click();await page.getByRole('dialog').getByRole('button',{name:'保存并切换',exact:true}).click();await page.getByRole('heading',{name:'正在查看：改名后',exact:true}).waitFor();assert.equal(bound,'fresh');
 // Switch to another role while discarding edits, then delete the now-unbound role.
 await page.getByLabel('智能体名称',{exact:true}).fill('丢弃');await page.locator('.role-card').filter({has:page.getByRole('button',{name:'我的角色',exact:true})}).getByRole('button',{name:'切换到此角色',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'放弃修改并切换',exact:true}).click();await page.getByRole('heading',{name:'正在查看：我的角色',exact:true}).waitFor();assert.equal(bound,'custom');assert.equal(fresh.name,'改名后');
 await page.getByRole('button',{name:'改名后',exact:true}).click();await page.getByRole('button',{name:'删除角色',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'取消',exact:true}).click();assert(fresh);
 await page.getByRole('button',{name:'删除角色',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'确认删除',exact:true}).click();await page.getByRole('button',{name:'改名后',exact:true}).waitFor({state:'hidden'});assert.equal(fresh,undefined);
 assert.deepEqual(errors,[]);console.log('PASS: create, save, discard/cancel, failed save blocks switch, save-and-switch, discard-and-switch, delete cancel/success');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
