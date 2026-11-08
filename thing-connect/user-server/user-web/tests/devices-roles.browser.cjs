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

 let bound='preset', role={id:'custom',name:'我的角色',agent_config:{prompt:'设定',mcp_tools:['missing'],device_plugins:[]},service_config:{type:'external',config:{opaque:'keep'}},user_params:{description:'测试描述',role_type_id:'type-1'}};
 let renameRelease,renameWait;
 await page.route('**/v1/**',async route=>{const req=route.request(),u=new URL(req.url());if(u.pathname.endsWith('/language.js'))return route.continue();requests.push({path:u.pathname,body:req.postDataJSON(),method:req.method()});let data={};
 if(u.pathname==='/v1/user/device/list')data=[{device_id:'one',device_name:'设备一',status:1,online:true,mac:'AA:BB:CC:DD:EE:FF',hardware:{chip_model:'x86_64',board_model:'thingconnect-device-sim-py'},firmware_version:'1.0.0',bind_time:'2026-09-09T15:08:56',profiles:{stream:{up_audio_mt:'alaw',up_video_mt:'h264',down_audio_mt:'alaw',down_video_mt:'none',up_audio_streamid:10,up_video_streamid:11,down_audio_streamid:14,down_video_streamid:15,audio_rate:16000,audio_channels:1,camera_rotation:0,hor_mirror:false,vert_mirror:false,aspect_ratio:'16:9',object_fit:'contain'}}},{device_id:'two',device_name:'设备二',status:1}];
 else if(u.pathname==='/v1/user/me')data={email:'test@example.test'};
 else if(u.pathname==='/v1/user/device/name'){if(renameWait)await renameWait;}
 else if(u.pathname==='/v1/ai/roles')data={items:[role]};
 else if(u.pathname==='/v1/ai/roles/default')data={id:'preset',name:'预设角色',agent_config:{prompt:'只读'}};
 else if(u.pathname==='/v1/ai/roles/custom'){if(req.method()==='PUT')role={...role,...req.postDataJSON()};if(req.method()==='DELETE')return route.fulfill({status:409,json:{code:40900,msg:'角色正在使用，请先解除设备关联',data:{devices:[{device_id:'two',device_name:'设备二'}]}}});data=role;}
 else if(u.pathname.includes('/v1/ai/device/')){if(req.method()==='PUT')bound=req.postDataJSON().role_id;if(req.method()==='DELETE')bound='preset';data={role_id:bound==='preset'?'':bound,default_role_id:'preset'};}
 else if(u.pathname.includes('/v1/recognition/devices/'))data={policy:{enabled:false},schemes:[]};
 else if(u.pathname.includes('/v1/call/group/web/device/'))data={desired_state:'left',state:'left',online:true};
 else if(u.pathname==='/v1/ai/role-types')data={items:[{id:'type-1',name:'温和伙伴',voice_id:'v',voice_model:'seed-tts-2.0',prompt:'设定',revision:1}]};
 else if(u.pathname==='/v1/ai/voices')data={items:[]};
 else if(u.pathname.includes('/v1/ai/'))data={items:[]};
 await route.fulfill({json:{code:200,data}});
 });
 await page.goto(base+'/login');await page.evaluate(()=>localStorage.setItem('token','test'));
 await page.goto(base+'/devices');await page.getByText('预设角色（默认）',{exact:true}).first().waitFor();const before=requests.length;
 await page.evaluate(()=>{dispatchEvent(new Event('focus'));document.dispatchEvent(new Event('visibilitychange'));});await page.waitForTimeout(1100);assert.equal(requests.length,before);
 const first=page.locator('[data-device-id="one"]'),second=page.locator('[data-device-id="two"]');
 await first.getByText('更多 ▾').click();await page.getByRole('menuitem',{name:'设备信息',exact:true}).click();await page.locator('.ant-dropdown:visible').waitFor({state:'hidden'});await page.getByText('G.711 A-law (alaw)',{exact:true}).first().waitFor();
 await page.getByText('否 (false)',{exact:true}).first().waitFor();await page.getByText('保持比例，完整显示 (contain)',{exact:true}).waitFor();await page.getByText('查看原始媒体参数',{exact:true}).click();assert((await page.locator('.device-raw-json').innerText()).includes('"up_audio_mt": "alaw"'));await page.getByText('查看原始媒体参数',{exact:true}).click();
 for(const width of [390,1366]){await page.setViewportSize({width,height:768});await page.screenshot({path:root+'/../output/playwright/device-info-compact-'+width+'.png'});assert(await page.getByRole('dialog').evaluate(e=>e.scrollWidth<=e.clientWidth));}
 await page.getByRole('button',{name:'关闭弹窗',exact:true}).click();
 await first.getByText('更多 ▾').click();await page.getByRole('menuitem',{name:'修改名称',exact:true}).waitFor();await page.waitForTimeout(300);await page.screenshot({path:root+'/../output/playwright/device-more-menu.png',animations:'disabled'});await page.getByRole('menuitem',{name:'修改名称',exact:true}).click();await page.locator('.ant-dropdown:visible').waitFor({state:'hidden'});await page.getByLabel('设备名称',{exact:true}).fill('新设备一');renameWait=new Promise(r=>renameRelease=r);await page.getByRole('button',{name:'保存名称',exact:true}).click();await page.getByRole('button',{name:'关闭弹窗'}).click();await second.getByText('更多 ▾').click();await page.getByRole('menuitem',{name:'修改名称',exact:true}).click();await page.locator('.ant-dropdown:visible').waitFor({state:'hidden'});await page.getByLabel('设备名称',{exact:true}).fill('设备二未保存');renameRelease();await page.waitForTimeout(150);assert.equal(await page.getByLabel('设备名称',{exact:true}).inputValue(),'设备二未保存');await page.getByRole('button',{name:'关闭弹窗'}).click();
 await page.goto(base+'/agent?device_id=one');await page.getByText('这是系统预设，只能查看。需要修改时请新建自己的角色。').waitFor();assert(await page.locator('.active-role').getByText('预设角色',{exact:true}).isVisible());
 await page.getByRole('button',{name:'我的角色',exact:true}).click();await page.getByLabel('智能体名称',{exact:true}).fill('编辑名称');await page.getByRole('button',{name:'切换到此角色',exact:true}).click();await page.getByRole('dialog').waitFor();await page.getByRole('button',{name:'继续编辑',exact:true}).click();assert.equal(bound,'preset');assert.equal(await page.getByLabel('智能体名称',{exact:true}).inputValue(),'编辑名称');
 await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.getByText('保存成功',{exact:true}).waitFor();assert.deepEqual(role.service_config,{type:'external',config:{opaque:'keep'}});assert.deepEqual(role.agent_config.mcp_tools,['missing']);assert.equal(role.user_params.description,'测试描述');assert.equal(await page.locator('.role-list').getByText('测试描述',{exact:true}).count(),0);await page.locator('.role-list').getByText('类型：温和伙伴',{exact:true}).waitFor();
 await page.getByRole('button',{name:'切换到此角色',exact:true}).click();await page.locator('.active-role').getByText('编辑名称',{exact:true}).waitFor();assert.equal(bound,'custom');await page.getByRole('status').filter({hasText:'已切换到“编辑名称”'}).waitFor();
 await page.getByRole('radio',{name:'扩展能力',exact:true}).locator('..').click();await page.getByRole('button',{name:'角色更多操作'}).click();assert.equal(await page.getByRole('menuitem').getAttribute('aria-disabled'),'true');await page.keyboard.press('Escape');await page.locator('.role-card').filter({has:page.getByRole('button',{name:'预设角色',exact:true})}).getByRole('button',{name:'切换到此角色'}).click();await page.getByRole('button',{name:'编辑名称',exact:true}).click();await page.getByRole('button',{name:'角色更多操作'}).click();await page.getByRole('menuitem',{name:'删除角色',exact:true}).click();await page.getByRole('button',{name:'确认删除',exact:true}).click();await page.getByRole('heading',{name:'暂时无法删除角色'}).waitFor();assert(await page.getByRole('dialog').getByText(/设备二/).isVisible());await page.getByRole('button',{name:'知道了'}).click();
 await page.locator('.ant-modal-wrap:visible').waitFor({state:'hidden'});await page.locator('.ant-dropdown:visible').waitFor({state:'hidden'});
 for(const width of [320,375,390,768,1440]){await page.setViewportSize({width,height:850});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'role overflow '+width+' '+JSON.stringify(await page.evaluate(()=>[...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right>innerWidth+1 && e.getBoundingClientRect().width).map(e=>[e.tagName,e.className,e.getBoundingClientRect().right]).slice(-15))));await page.screenshot({path:root+'/../output/playwright/react-role-'+width+'.png',fullPage:true});}
 assert.deepEqual(errors,[]);console.log('PASS: devices no polling, late rename modal isolation; roles preset readonly, active state, dirty switching, opaque config and unavailable references preserved, in-use deletion guard, 5 mobile/desktop widths. Requests='+requests.length);
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
