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
 await page.goto(base+'/devices');await page.getByText('预设角色（默认）',{exact:true}).first().waitFor();const before=requests.length;
 await page.evaluate(()=>{dispatchEvent(new Event('focus'));document.dispatchEvent(new Event('visibilitychange'));});await page.waitForTimeout(1100);assert.equal(requests.length,before);
 const first=page.locator('[data-device-id="one"]'),second=page.locator('[data-device-id="two"]');
 await first.getByText('更多 ▾').click();await page.getByRole('button',{name:'修改名称',exact:true}).click();await page.locator('.ant-popover:visible').waitFor({state:'hidden'});await page.getByLabel('设备名称',{exact:true}).fill('新设备一');renameWait=new Promise(r=>renameRelease=r);await page.getByRole('button',{name:'保存名称',exact:true}).click();await page.getByRole('button',{name:'关闭弹窗'}).click();await second.getByText('更多 ▾').click();await page.getByRole('button',{name:'修改名称',exact:true}).click();await page.locator('.ant-popover:visible').waitFor({state:'hidden'});await page.getByLabel('设备名称',{exact:true}).fill('设备二未保存');renameRelease();await page.waitForTimeout(150);assert.equal(await page.getByLabel('设备名称',{exact:true}).inputValue(),'设备二未保存');await page.getByRole('button',{name:'关闭弹窗'}).click();
 await page.goto(base+'/agent?device_id=one');await page.getByText('这是系统预设，只能查看。需要修改时请新建自己的角色。').waitFor();assert(await page.locator('.active-role').getByText('预设角色',{exact:true}).isVisible());
 await page.getByRole('button',{name:'我的角色',exact:true}).click();await page.getByLabel('智能体名称',{exact:true}).fill('编辑名称');await page.getByRole('button',{name:'切换到此角色',exact:true}).click();await page.getByRole('dialog').waitFor();await page.getByRole('button',{name:'继续编辑',exact:true}).click();assert.equal(bound,'preset');assert.equal(await page.getByLabel('智能体名称',{exact:true}).inputValue(),'编辑名称');
 await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.getByText('保存成功',{exact:true}).waitFor();assert.deepEqual(role.service_config,{type:'external',config:{opaque:'keep'}});assert.deepEqual(role.agent_config.mcp_tools,['missing']);
 await page.getByRole('button',{name:'切换到此角色',exact:true}).click();await page.locator('.active-role').getByText('编辑名称',{exact:true}).waitFor();assert.equal(bound,'custom');await page.getByRole('status').filter({hasText:'已切换到“编辑名称”'}).waitFor();
 await page.getByRole('radio',{name:'扩展能力',exact:true}).locator('..').click();await page.getByRole('button',{name:'删除角色',exact:true}).click();await page.getByRole('button',{name:'确认删除',exact:true}).click();await page.getByRole('heading',{name:'暂时无法删除角色'}).waitFor();assert(await page.getByRole('dialog').getByText(/设备二/).isVisible());await page.getByRole('button',{name:'知道了'}).click();
 for(const width of [320,375,390,768,1440]){await page.setViewportSize({width,height:850});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'role overflow '+width);await page.screenshot({path:root+'/../output/playwright/react-role-'+width+'.png',fullPage:true});}
 assert.deepEqual(errors,[]);console.log('PASS: devices no polling, late rename modal isolation; roles preset readonly, active state, dirty switching, opaque config and unavailable references preserved, in-use deletion guard, 5 mobile/desktop widths. Requests='+requests.length);
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
