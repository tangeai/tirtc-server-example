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

 await page.goto(base+'/devices');
 const card=page.locator('[data-device-id="one"]');
 const roomButton=card.locator('.device-actions').getByRole('button',{name:'多人对讲',exact:true});
 await roomButton.click();
 let dialog=page.getByRole('dialog',{name:'多人对讲',exact:true});
 await dialog.getByRole('button',{name:'创建房间',exact:true}).last().waitFor();
 assert.equal(new URL(page.url()).pathname,'/devices');
 for(const viewport of [{width:1366,height:768},{width:760,height:720},{width:390,height:844},{width:320,height:700}]){
  await page.setViewportSize(viewport);
  await dialog.getByRole('navigation',{name:'房间操作'}).getByRole('button',{name:'加入房间',exact:true}).click();
  const metrics=await dialog.evaluate(el=>({h:el.scrollHeight,c:el.clientHeight,w:el.scrollWidth,cw:el.clientWidth}));
  assert.ok(metrics.h<=metrics.c+1,'room must fit vertically '+JSON.stringify({viewport,metrics}));
  assert.ok(metrics.w<=metrics.cw+1,'room overflow');
  if(viewport.width>=640){const bounds=await dialog.boundingBox();assert.ok(bounds.width<=480&&bounds.height<=480,'compact room footprint');}
  await page.screenshot({path:path.join(root,'../output/playwright/device-room-'+viewport.width+'.png')});
 }
 await dialog.getByLabel('6 位房间号').fill('001234');
 failChange=true;
 await dialog.getByRole('button',{name:'加入房间',exact:true}).last().click();
 await dialog.getByRole('alert').waitFor();assert.equal(await dialog.getByLabel('6 位房间号').inputValue(),'001234');
 await dialog.getByRole('button',{name:'加入房间',exact:true}).last().click();
 await dialog.getByText('001234',{exact:true}).waitFor();
 await dialog.getByRole('button',{name:'关闭弹窗'}).click();
 assert.ok((await card.innerText()).includes('001234'),'parent summary follows room action');
 assert.equal(await roomButton.evaluate(el=>el===document.activeElement),true,'focus returns to trigger');
 const readCount=requests.filter(x=>x.path==='/v1/user/device/list').length;
 await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));document.dispatchEvent(new Event('visibilitychange'));});
 assert.equal(requests.filter(x=>x.path==='/v1/user/device/list').length,readCount);
 let contacts=Array.from({length:40},(_,i)=>({id:i+1,type:'device',source:'manual',device_id:'peer'+i,device_name:'联系人'+i}));
 await page.route('**/v1/call/user/contacts**',async route=>{const req=route.request(),url=new URL(req.url());let data={};if(url.pathname.endsWith('/pending'))data={pending:[]};else if(url.pathname.endsWith('/remark')){const body=req.postDataJSON();contacts=contacts.map(c=>c.device_id===body.peer_id?{...c,remark:body.remark}:c);}else data={contacts};await route.fulfill({json:{code:200,data}});});
 await card.getByRole('button',{name:'联系人',exact:true}).click();
 dialog=page.getByRole('dialog',{name:'联系人',exact:true});
 await dialog.getByRole('button',{name:'改备注'}).first().waitFor();
 for(const viewport of [{width:1366,height:768},{width:760,height:720},{width:390,height:844},{width:320,height:700}]){
  await page.setViewportSize(viewport);
  const metrics=await dialog.evaluate(el=>{const list=el.querySelector('.contact-scroll');return{h:el.scrollHeight,c:el.clientHeight,w:el.scrollWidth,cw:el.clientWidth,list:list.scrollHeight>list.clientHeight};});
  assert.ok(metrics.h<=metrics.c+1,'contact dialog should not scroll '+JSON.stringify({viewport,metrics}));assert.ok(metrics.w<=metrics.cw+1,'contact horizontal overflow');assert.ok(metrics.list,'list scrolls internally');if(viewport.width>=640){const bounds=await dialog.boundingBox();assert.ok(bounds.width<=640&&bounds.height<=622,'compact contact footprint');}
  await page.screenshot({path:path.join(root,'../output/playwright/device-contacts-'+viewport.width+'.png')});
 }
 await page.setViewportSize({width:1366,height:768});
 await dialog.getByRole('searchbox').fill('联系人0');
 assert.ok((await dialog.boundingBox()).height<500,'short contact results shrink the dialog');
 await dialog.getByRole('button',{name:'改备注'}).click();await dialog.getByLabel('联系人备注').fill('新备注');await dialog.getByRole('button',{name:'保存',exact:true}).click();await dialog.getByText('新备注',{exact:true}).waitFor();
 await dialog.getByRole('button',{name:'添加联系人'}).click();
 const child=page.getByRole('dialog',{name:'添加联系人',exact:true});await child.waitFor();await page.keyboard.press('Escape');await child.waitFor({state:'hidden'});assert.equal(await dialog.isVisible(),true);
 await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'});assert.equal(await page.evaluate(()=>document.body.style.overflow),'');
 await card.getByRole('button',{name:'联系人',exact:true}).click();await page.getByRole('dialog',{name:'联系人',exact:true}).getByRole('searchbox').waitFor();assert.equal(await page.getByRole('searchbox').inputValue(),'');await page.keyboard.press('Escape');
 // A media dialog must release audio/video resources before another device opens.
 await page.route('**/static/js/tirtc.es.min.js',route=>route.fulfill({contentType:'text/javascript',body:`
 window.mediaCalls=[];
 export const TiRtc={initialize(){},async videoOutputReady(){}};
 export function TiRtcInitOptions(v){return v;}
 export class TiRtcConn{async connect(v){window.mediaCalls.push('connect:'+v.deviceId);} disconnect(){window.mediaCalls.push('disconnect');} subscribeAudio(){} unsubscribeAudio(){} subscribeVideo(){} unsubscribeVideo(){}}
 export function TiRtcAudioOutput(){return {attach(){},detach(){window.mediaCalls.push('detach-audio');}};}
 export function TiRtcVideoOutput(){return {attach(){},detach(){window.mediaCalls.push('detach-video');}};}
 export class TiRtcAudioInput{setOptions(){} attach(){} detach(){} async start(){window.mediaCalls.push('talk');} stop(){window.mediaCalls.push('stop-talk');}}
 `}));
 await page.route('**/v1/user/device/rtc-token?*',route=>route.fulfill({json:{code:200,data:{token:'fixture',app_id:'fixture'}}}));
 await card.getByRole('button',{name:'实时',exact:true}).click();
 dialog=page.getByRole('dialog',{name:'实时查看',exact:true});await dialog.getByText('已连接 · 点击声音按钮开启或关闭声音',{exact:true}).waitFor();
 for(const viewport of [{width:1366,height:768},{width:390,height:844},{width:320,height:700}]){await page.setViewportSize(viewport);assert.ok(await dialog.evaluate(el=>el.scrollHeight<=el.clientHeight+1&&el.scrollWidth<=el.clientWidth+1),'player fits dialog');await page.screenshot({path:path.join(root,'../output/playwright/device-player-'+viewport.width+'.png')});}
 await dialog.getByRole('button',{name:'开启声音'}).click();await dialog.getByRole('button',{name:'按住说话'}).focus();await page.keyboard.down('Space');await dialog.getByRole('button',{name:'说话中…'}).waitFor();await page.keyboard.press('Escape');await page.keyboard.up('Space');await dialog.waitFor({state:'hidden'});
 const mediaCalls=await page.evaluate(()=>window.mediaCalls);assert.ok(mediaCalls.includes('disconnect'));assert.ok(mediaCalls.includes('detach-video'));assert.ok(mediaCalls.includes('detach-audio'));assert.ok(mediaCalls.includes('stop-talk'));
 assert.equal(await card.getByRole('link',{name:'AI 角色',exact:true}).count(),1);
 assert.deepEqual(errors,[]);console.log('PASS device dialogs: one-screen room, internal contact scrolling at four sizes, failed join retry, summary update, focus restore, nested Escape, reopen, no automatic refresh');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
