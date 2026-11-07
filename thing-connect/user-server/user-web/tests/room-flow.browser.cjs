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
 let assignment={desired_state:'left',state:'left',online:true},failChange=false,failRead=false,writes=[];
 await page.route('**/v1/call/group/web/device/**',async route=>{const req=route.request(),u=new URL(req.url());if(req.method()==='POST'){
  writes.push({action:u.pathname.split('/').pop(),body:req.postDataJSON()});
  if(failChange){failChange=false;return route.fulfill({status:400,json:{code:4001,msg:'房间密码错误'}});}
  const action=u.pathname.split('/').pop(),body=req.postDataJSON();assignment=action==='leave'?{desired_state:'left',state:'left',online:true}:{desired_state:'joined',state:'waiting_device',room_id:7,assignment_version:9,room_code:body.room_code||'012345',password_set:!!body.password,online:false,online_count:0};
 }else if(failRead){failRead=false;return route.fulfill({status:503,json:{code:50000,msg:'状态读取失败'}});}
 await route.fulfill({json:{code:200,data:assignment}});
 });
 await page.setViewportSize({width:1440,height:900});await page.goto(base+'/room?device_id=one');await page.getByRole('button',{name:'创建房间',exact:true}).waitFor();
 await page.getByLabel('设置密码',{exact:true}).check();await page.getByLabel('4 位数字密码',{exact:true}).fill('ab12345');assert.equal(await page.getByLabel('4 位数字密码',{exact:true}).inputValue(),'1234');
 await page.getByRole('button',{name:'创建房间',exact:true}).click();await page.getByText('012345',{exact:true}).waitFor();assert.deepEqual(writes[0],{action:'create',body:{password:'1234'}});
 await page.getByRole('button',{name:'退出房间',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'取消',exact:true}).click();assert.equal(writes.length,1);
 await page.getByRole('button',{name:'退出房间',exact:true}).click();await page.getByRole('button',{name:'确认退出',exact:true}).click();await page.getByRole('button',{name:'加入房间',exact:true}).waitFor();assert.deepEqual(writes[1].body,{room_id:7,assignment_version:9});
 await page.getByLabel('6 位房间号').fill('123');await page.getByRole('button',{name:'加入房间',exact:true}).click();assert.equal(writes.length,2);
 await page.getByLabel('6 位房间号').fill('001234');await page.getByLabel('房间密码（无密码房间留空）').fill('1234');failChange=true;
 await page.getByRole('button',{name:'加入房间',exact:true}).click();await page.getByRole('alert').waitFor();assert.equal(await page.getByLabel('6 位房间号').inputValue(),'001234');
 failRead=true;await page.locator('form:last-of-type button[type=submit]:not(.ant-btn-loading)').click();await page.getByText('操作已成功，状态更新失败；请点击刷新获取最新状态。',{exact:true}).waitFor();await page.getByRole('button',{name:'刷新',exact:true}).click();await page.getByText('设备离线',{exact:true}).waitFor();
 for(const state of ['assigned','suspended','connecting','joined','connect_failed']){assignment={...assignment,state,online:true,online_count:2};await page.getByRole('button',{name:'刷新',exact:true}).click();await page.waitForFunction(()=>!document.querySelector('button:disabled'));assert.equal(await page.getByRole('button',{name:'退出房间',exact:true}).count(),1);}
 assignment={desired_state:'left',state:'room_closed',online:true};await page.getByRole('button',{name:'刷新',exact:true}).click();await page.getByText('房间已解散，可新建或加入其他房间',{exact:true}).waitFor();
 console.log('PASS: room create/password validation, join/error/retry, leave cancel/versioned request, failed follow-up refresh, all assignment states');
 assert.deepEqual(errors,[]);console.log('Room browser errors: 0');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
