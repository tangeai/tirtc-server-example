const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs=require('fs'),path=require('path'),http=require('http'),assert=require('assert/strict');
const root=path.resolve(__dirname,'../../..');
fs.mkdirSync(path.join(root,'../output/playwright'),{recursive:true});
const requests=[],errors=[];let voiceMode='ok';
const server=http.createServer((req,res)=>{const url=new URL(req.url,'http://localhost');let file;
 if(url.pathname==='/v1/config/language.js'){res.setHeader('Content-Type','text/javascript');res.end('window.WebLanguageConfig={default:"zh-CN",allow_switch:true};'+fs.readFileSync(root+'/internal/webui/runtime.js','utf8')+';WebI18n.addMessages('+fs.readFileSync(root+'/internal/webui/common.json','utf8')+');');return;}
 file=url.pathname.startsWith('/static/')?root+'/user-server'+url.pathname:root+'/user-server/static/app/index.html';
 if(!fs.existsSync(file)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM || undefined,args:['--no-sandbox']});
 try{
 const context=await browser.newContext();const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));

 let bound='preset', role={id:'custom',name:'我的角色',agent_config:{prompt:'设定',mcp_tools:['missing'],device_plugins:[]},service_config:{type:'custom-compose',config:{opaque:'keep'}},user_params:{description:'测试描述'}};
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
 else if(u.pathname==='/v1/ai/voices'){
 if(voiceMode==='error')return route.fulfill({status:503,json:{code:50000,msg:'声音暂不可用'}});
 data={items:voiceMode==='empty'?[]:[{id:'zh',name:'中文测试声音',languages:['zh-CN'],model:'cosyvoice-v3-flash',sample_url:'/preview.mp3'},{id:'en',name:'English Voice',languages:['en-US'],model:'cosyvoice-v3-flash'},{id:'other',name:'其他引擎',languages:['zh'],model:'other'}]};
 }
 else if(u.pathname.includes('/v1/ai/'))data={items:[]};
 await route.fulfill({json:{code:200,data}});
 });
 await page.goto(base+'/login');await page.evaluate(()=>localStorage.setItem('token','test'));
 await page.setViewportSize({width:1440,height:1000});
 const kp='/v1/ai/knowledge/indexes';
 let indexes=[{id:'k1',name:'知识库一',description:'测试知识库',document_ids:[]},{id:'k2',name:'知识库二',description:'第二个',document_ids:[]}];
 let serial=0,files=[],failUpload=false,failIndex=false,failDocs=false;
 await page.route('**/v1/ai/knowledge/**',async route=>{
  const req=route.request(),u=new URL(req.url()),id=u.pathname.slice(kp.length+1).split('/')[0],method=req.method();let data={};
  if(u.pathname==='/v1/ai/knowledge/files'){
   if(failUpload){failUpload=false;return route.fulfill({status:503,json:{code:50000,msg:'上传失败，请重试'}});}
   const file={document_id:'file'+(++serial),name:'测试文件'+serial+'.txt',size:100};files.push(file);data={file_id:file.document_id};
  }else if(u.pathname.startsWith('/v1/ai/knowledge/files/')){
   const fid=u.pathname.split('/').pop();files=files.filter(f=>f.document_id!==fid);indexes.forEach(k=>k.document_ids=k.document_ids.filter(x=>x!==fid));
  }else if(u.pathname===kp){
   if(method==='POST'){const k={...req.postDataJSON(),id:'k'+(++serial+2),document_ids:[]};indexes.push(k);data={index_id:k.id};}
   else data={items:indexes};
  }else if(u.pathname.endsWith('/documents')){
   if(failDocs){failDocs=false;return route.fulfill({status:503,json:{code:50000,msg:'读取失败'}});}
   const k=indexes.find(x=>x.id===id),items=files.filter(f=>k.document_ids.includes(f.document_id));data={items,total:items.length};
  }else if(method==='PUT'){
   if(failIndex){failIndex=false;return route.fulfill({status:503,json:{code:50000,msg:'保存失败，请重试'}});}
   indexes=indexes.map(k=>k.id===id?{...k,...req.postDataJSON()}:k);data={index_id:id};
  }else if(method==='DELETE')indexes=indexes.filter(k=>k.id!==id);
  else data=indexes.find(item=>item.id===id);
  await route.fulfill({json:{code:200,data}});
 });
 const resources={mcp:[{id:'m1',name:'自有工具',enabled:true,config:{name:'自有工具',description:'测试',url:'https://example.test/mcp',authentication:null}}],plugin:[{id:'p1',name:'自有插件',description:'测试',action:'test',input_params:[],return_params:[]}]};
 const rp={mcp:'/v1/ai/mcp/app-tools',plugin:'/v1/ai/plugins'};
 for(const kind of ['mcp','plugin'])await page.route('**'+rp[kind]+'**',async route=>{
  const req=route.request(),u=new URL(req.url()),id=u.pathname.slice(rp[kind].length+1),method=req.method();let data={};
  if(method==='POST'){const body=req.postDataJSON(),item={...body,id:kind+(++serial),name:body.config?.name||body.name};resources[kind].push(item);data=item;}
  else if(method==='PUT'){resources[kind]=resources[kind].map(item=>item.id===id?{...item,...req.postDataJSON(),name:req.postDataJSON().config?.name||req.postDataJSON().name}:item);data=resources[kind].find(item=>item.id===id);}
  else if(method==='DELETE')resources[kind]=resources[kind].filter(item=>item.id!==id);
  else data=id?resources[kind].find(item=>item.id===id):{items:resources[kind]};
  await route.fulfill({json:{code:200,data}});
 });
 resources.plugin[0].input_params=[{name:'message',type:'string',default_value:'  keep spaces  ',required:false},{type:'integer',default_value:'3'}];
 delete resources.plugin[0].description;
 await page.goto(base+'/agent?device_id=one');await page.getByRole('button',{name:'我的角色',exact:true}).click();await page.getByRole('button',{name:'扩展能力',exact:true}).click();await page.locator('summary').filter({hasText:'工具与插件'}).click();
 const row=page.getByRole('checkbox',{name:'自有插件',exact:true}).locator('xpath=../..');await row.getByRole('button',{name:'编辑',exact:true}).click();const dialog=page.getByRole('dialog');await dialog.locator('.param-card').first().waitFor();
 await dialog.getByLabel('名称',{exact:true}).fill('改名');await dialog.getByRole('button',{name:'保存',exact:true}).click();await page.waitForTimeout(150);
 assert.deepEqual(errors,[],'omitted optional descriptions must not crash save');await dialog.waitFor({state:'hidden'});assert.equal(resources.plugin[0].input_params[0].default_value,'  keep spaces  ','preserve meaningful whitespace');assert.equal(resources.plugin[0].input_params.length,2,'preserve optional-name legacy parameters');
 // Close guards cover X, Escape and explicit cancel; a clean editor closes directly.
 const pluginRow=()=>page.getByRole('checkbox',{name:resources.plugin[0].name,exact:true}).locator('xpath=../..');
 await pluginRow().getByRole('button',{name:'编辑',exact:true}).click();const editor=page.getByRole('dialog',{name:'编辑设备端插件',exact:true});await editor.getByLabel('名称',{exact:true}).fill('未保存的名字');
 for(const action of ['x','escape','cancel']){
  if(action==='x')await editor.getByRole('button',{name:'关闭弹窗',exact:true}).click();else if(action==='escape')await page.keyboard.press('Escape');else await editor.getByRole('button',{name:'取消',exact:true}).click();
  const confirm=page.getByRole('dialog',{name:'修改尚未保存',exact:true});await confirm.waitFor();assert(await editor.isVisible());await confirm.getByRole('button',{name:'继续编辑',exact:true}).click();assert.equal(await editor.getByLabel('名称',{exact:true}).inputValue(),'未保存的名字');
 }
 await page.keyboard.press('Escape');await page.getByRole('dialog',{name:'修改尚未保存',exact:true}).getByRole('button',{name:'放弃修改',exact:true}).click();await editor.waitFor({state:'hidden'});assert.equal(resources.plugin[0].name,'改名');
 await pluginRow().getByRole('button',{name:'编辑',exact:true}).click();await editor.locator('.param-card').first().waitFor();await editor.getByRole('button',{name:'关闭弹窗',exact:true}).click();assert.equal(await page.getByRole('dialog').count(),0);
 let fail=true,release,wait,writeCount=0;
 await page.route('**/v1/ai/plugins/p1',async route=>{if(route.request().method()==='PUT'){writeCount++;if(fail){fail=false;return route.fulfill({status:503,json:{code:50000,msg:'保存失败，请重试'}});}if(wait)await wait;}await route.fallback();});
 await pluginRow().getByRole('button',{name:'编辑',exact:true}).click();await editor.getByLabel('名称',{exact:true}).fill('重试后保存');await editor.getByRole('button',{name:'保存',exact:true}).click();await editor.getByRole('alert').waitFor();assert.equal(await editor.getByLabel('名称',{exact:true}).inputValue(),'重试后保存');
 wait=new Promise(r=>release=r);await editor.getByRole('button',{name:'保存',exact:true}).click();await editor.getByRole('button',{name:'保存中…',exact:true}).evaluate(e=>e.click());assert(await editor.getByRole('button',{name:'关闭弹窗',exact:true}).isDisabled());await page.keyboard.press('Escape');assert(await editor.isVisible());assert.equal(writeCount,2);release();await editor.waitFor({state:'hidden'});assert.equal(resources.plugin[0].name,'重试后保存');
 // Failed deletion followed by success must clear the previous error.
 let rejectDelete=true;
 await page.route('**/v1/ai/plugins/p1',async route=>{
  if(route.request().method()==='DELETE'&&rejectDelete){rejectDelete=false;return route.fulfill({status:409,json:{code:40900,msg:'插件正在使用'}});}
  await route.fallback();
 });
 const pluginSection=page.locator('.resource-list').filter({has:page.getByRole('heading',{name:'设备插件',exact:true})});
 await pluginRow().getByRole('button',{name:'删除',exact:true}).click();await page.getByRole('button',{name:'确认删除',exact:true}).click();await pluginSection.getByRole('alert').waitFor();
 await pluginRow().getByRole('button',{name:'删除',exact:true}).click();await page.getByRole('button',{name:'确认删除',exact:true}).click();await page.getByRole('checkbox',{name:'重试后保存',exact:true}).waitFor({state:'hidden'});
 assert.equal(await pluginSection.getByRole('alert').count(),0,'successful deletion must clear its previous error');
 // A delayed deletion of A must not clear a newer selection of B.
 await page.locator('summary').filter({hasText:'知识库'}).click();
 const knowledge=page.locator('.role-section select').filter({has:page.locator('option[value="k2"]')});await knowledge.selectOption('k1');
 const knowledgeSection=knowledge.locator('xpath=../..');
 let releaseDelete;const deleteWait=new Promise(r=>releaseDelete=r);
 await page.route('**/v1/ai/knowledge/indexes/k1',async route=>{if(route.request().method()==='DELETE')await deleteWait;await route.fallback();});
 await knowledgeSection.getByRole('button',{name:'删除',exact:true}).click();await page.getByRole('button',{name:'确认删除',exact:true}).click();
 assert(await knowledgeSection.getByRole('button',{name:'删除',exact:true}).isDisabled());
 await knowledge.selectOption('k2');releaseDelete();
 await page.waitForResponse(r=>r.url().endsWith('/indexes/k1')&&r.request().method()==='DELETE');await page.waitForTimeout(100);
 assert.equal(await knowledge.inputValue(),'k2','deleting previous index must preserve the current selection');
 assert.deepEqual(errors,[]);console.log('PASS: optional descriptions, exact defaults, X/Escape/cancel draft guards, clean close, failed-save retry, busy close and duplicate-submit protection; deletion retry clears errors; delayed deletion preserves newer knowledge selection');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
