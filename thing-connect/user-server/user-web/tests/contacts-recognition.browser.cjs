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
 let contacts=[{id:1,type:'device',source:'manual',device_id:'peer',device_name:'朋友',online:true},{id:2,type:'device',source:'auto',device_id:'two',device_name:'设备二'},{id:3,type:'voip',source:'auto',device_id:'wx',device_name:'微信用户'}],pending=[{id:10,initiator_device:'applicant',target_device:'one'},{id:11,initiator_device:'reject',target_device:'one'}],actions=[];
 await page.route('**/v1/call/user/contacts**',async route=>{const req=route.request(),u=new URL(req.url());const body=req.postDataJSON();let data={};
  if(u.pathname.endsWith('/respond')){actions.push(body);pending=pending.filter(p=>p.id!==body.id);}
  else if(u.pathname.endsWith('/pending'))data={pending};
  else if(u.pathname.endsWith('/remark'))contacts=contacts.map(c=>c.device_id===body.peer_id?{...c,remark:body.remark}:c);
  else if(u.pathname.endsWith('/request')){actions.push(body);data={status:'pending'};}
  else if(req.method()==='DELETE')contacts=contacts.filter(c=>c.id!==Number(u.pathname.split('/').pop()));
  else data={contacts};await route.fulfill({json:{code:200,data}});
 });
 await page.goto(base+'/contacts?device_id=one');await page.getByRole('button',{name:'改备注',exact:true}).first().waitFor();
 await page.getByRole('radio',{name:'微信授权',exact:true}).locator('..').click();assert.equal(await page.getByRole('button',{name:'改备注',exact:true}).count(),1);assert.equal(await page.getByRole('button',{name:'删除',exact:true}).count(),0);
 await page.getByRole('radio',{name:'我的设备',exact:true}).locator('..').click();assert.equal(await page.getByRole('button',{name:'删除',exact:true}).count(),0);await page.getByRole('radio',{name:'全部',exact:true}).locator('..').click();
 await page.getByRole('searchbox').fill('不存在');await page.getByText('没有匹配的联系人',{exact:true}).waitFor();await page.getByRole('button',{name:'清除搜索和筛选',exact:true}).click();
 await page.getByRole('button',{name:'改备注',exact:true}).first().click();await page.getByLabel('联系人备注').fill('备注朋友');await page.getByLabel('联系人备注').press('Enter');await page.getByText('备注朋友',{exact:true}).waitFor();assert.equal(contacts[0].remark,'备注朋友');
 await page.getByRole('button',{name:'改备注',exact:true}).first().click();await page.getByLabel('联系人备注').fill('');await page.getByRole('button',{name:'保存',exact:true}).click();await page.getByText('朋友',{exact:true}).waitFor();
 await page.getByRole('button',{name:'添加联系人',exact:true}).click();for(const value of ['one','two','peer']){await page.getByLabel('对方设备 ID').fill(value);await page.getByRole('button',{name:'发起申请',exact:true}).click();await page.getByRole('dialog').getByRole('alert').waitFor();}assert.equal(actions.length,0);
 await page.getByLabel('对方设备 ID').fill('other');await page.getByRole('button',{name:'发起申请',exact:true}).click();await page.getByText('申请已发送，等待对方同意',{exact:true}).waitFor();await page.getByRole('button',{name:'关闭弹窗',exact:true}).click();
 await page.getByRole('button',{name:'同意',exact:true}).first().click();await page.waitForFunction(()=>document.querySelectorAll('.pending-row').length===1);await page.getByRole('button',{name:'拒绝',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('.pending-row').length===0);assert.deepEqual(actions.slice(1).map(x=>x.action),['accept','reject']);
 await page.getByRole('button',{name:'删除',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'删除',exact:true}).click();await page.getByRole('dialog').waitFor({state:'hidden'});assert.equal(contacts.length,2);
 await page.goto(base+'/devices');await page.locator('[data-device-id="one"]').getByRole('button',{name:'联系人',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'改备注',exact:true}).first().click();await page.getByLabel('联系人备注').fill('未保存备注');
 for(const width of [390,1366]){await page.setViewportSize({width,height:844});await page.screenshot({path:root+'/../output/playwright/contacts-remark-'+width+'.png'});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));}
 await page.getByLabel('联系人备注').press('Escape');await page.getByLabel('联系人备注').waitFor({state:'hidden'});assert(await page.getByRole('dialog').isVisible());assert(!contacts.some(c=>c.remark==='未保存备注'));await page.getByRole('button',{name:'关闭弹窗',exact:true}).click();
 // Recognition configuration, ordered selection, revision conflict, reload and save.
 const types=['声音识别','情绪识别','其他识别'].map((name,i)=>({scheme_id:'s'+i,scheme_version:1,name,output_mode:'text',limits:{max_audio_seconds:30}}));let config={policy:{enabled:false,revision:1,monthly_limit:100},schemes:[],usage:{used:0}},conflict=true,lastWrite;
 await page.route('**/v1/recognition/types',route=>route.fulfill({json:{code:200,data:types}}));
 await page.route('**/v1/recognition/devices/**',async route=>{if(route.request().method()==='PUT'){lastWrite=route.request().postDataJSON();if(conflict){conflict=false;return route.fulfill({status:409,json:{code:40901,msg:'配置已被修改'}});}config={...config,policy:{...config.policy,enabled:lastWrite.enabled,revision:2},schemes:lastWrite.schemes.map(s=>types.find(x=>x.scheme_id===s.scheme_id))};}await route.fulfill({json:{code:200,data:config}});});
 await page.goto(base+'/devices');await page.locator('[data-device-id="one"]').getByRole('button',{name:/AI识别/}).click();const dialog=page.getByRole('dialog');await dialog.getByLabel('开启AI识别').check();await dialog.getByRole('button',{name:'保存设置',exact:true}).click();await dialog.getByText('请至少选择一个识别类型',{exact:true}).waitFor();
 await dialog.getByRole('button',{name:'＋ 添加音频类型',exact:true}).click();assert(await dialog.getByRole('button',{name:'添加所选',exact:true}).isDisabled());await dialog.getByLabel('声音识别',{exact:true}).check();await dialog.getByLabel('情绪识别',{exact:true}).check();await dialog.getByRole('button',{name:'添加所选',exact:true}).click();await dialog.getByRole('button',{name:'识别类型更多操作：声音识别',exact:true}).click();await page.getByRole('menuitem',{name:'下移',exact:true}).click();
 await dialog.getByRole('button',{name:'保存设置',exact:true}).click();await dialog.getByText('设置已被其他页面修改，当前草稿已保留。',{exact:true}).waitFor();assert.deepEqual(lastWrite.schemes.map(s=>s.scheme_id),['s1','s0']);assert.equal(lastWrite.expected_revision,1);
 await dialog.getByRole('button',{name:'放弃草稿并重新加载',exact:true}).click();await page.waitForFunction(()=>document.querySelectorAll('.scheme').length===0);
 await dialog.getByLabel('开启AI识别').check();await dialog.getByRole('button',{name:'＋ 添加音频类型',exact:true}).click();assert(await dialog.getByRole('button',{name:'添加所选',exact:true}).isDisabled());await dialog.getByLabel('声音识别',{exact:true}).check();await dialog.getByRole('button',{name:'添加所选',exact:true}).click();for(const width of [390,1366]){await page.setViewportSize({width,height:844});await page.screenshot({path:root+'/../output/playwright/recognition-compact-'+width+'.png'});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));}
 await dialog.getByRole('button',{name:'保存设置',exact:true}).click();await dialog.waitFor({state:'hidden'});assert.equal(config.policy.enabled,true);assert.equal(config.schemes.length,1);
 // Exhausted catalogs never open an empty picker; removal makes the type available again.
 config.schemes=[...types];await page.locator('[data-device-id="one"]').getByRole('button',{name:/AI识别/}).click();await dialog.getByText('所有可用类型均已添加',{exact:true}).waitFor();assert.equal(await dialog.getByRole('button',{name:'＋ 添加音频类型',exact:true}).count(),0);assert.equal(await dialog.getByRole('button',{name:'添加所选',exact:true}).count(),0);
 await page.screenshot({path:root+'/../output/playwright/recognition-all-added.png'});
 await dialog.getByRole('button',{name:'识别类型更多操作：声音识别',exact:true}).click();await page.getByRole('menuitem',{name:'移除',exact:true}).click();await dialog.getByRole('button',{name:'＋ 添加音频类型',exact:true}).click();await dialog.getByLabel('声音识别',{exact:true}).check();await dialog.getByRole('button',{name:'添加所选',exact:true}).click();await dialog.getByText('所有可用类型均已添加',{exact:true}).waitFor();await dialog.getByRole('button',{name:'关闭弹窗',exact:true}).click();
 console.log('PASS: contacts filters/search/remarks/self-duplicate guards/apply/approve/reject/delete; recognition empty guard/add/order/revision conflict/reload/save');
 assert.deepEqual(errors,[]);console.log('Contacts and recognition browser errors: 0');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
