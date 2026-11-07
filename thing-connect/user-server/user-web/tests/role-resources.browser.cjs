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
 await page.goto(base+'/agent?device_id=one');await page.getByRole('button',{name:'我的角色',exact:true}).click();
 await page.getByRole('radio',{name:'扩展能力',exact:true}).locator('..').click();await page.locator('summary').filter({hasText:'知识库'}).click();
 const choice=page.locator('.role-section select').filter({has:page.locator('option[value="k1"]')});await choice.selectOption('k1');
 for(let i=0;i<3;i++){
  await page.getByRole('button',{name:'编辑知识库',exact:true}).click();await page.getByLabel('知识库名称',{exact:true}).waitFor();
  await page.getByRole('button',{name:'关闭弹窗',exact:true}).click();
  assert.equal(await page.locator('input[type=file]').count(),1,'one uploader after closing index editor');
 }
 // Edit-save and retry preserve draft, and only one document section survives.
 await page.getByRole('button',{name:'编辑知识库',exact:true}).click();await page.getByLabel('知识库名称',{exact:true}).fill('更新知识库');
 failIndex=true;await page.getByRole('dialog').getByRole('button',{name:'保存',exact:true}).click();await page.getByRole('dialog').getByRole('alert').waitFor();
 assert.equal(await page.getByLabel('知识库名称',{exact:true}).inputValue(),'更新知识库');
 await page.getByRole('dialog').getByRole('button',{name:'保存',exact:true}).click();await page.getByRole('dialog').waitFor({state:'hidden'});assert.equal(await page.locator('input[type=file]').count(),1);
 await choice.selectOption('k2');await choice.selectOption('k1');assert.equal(await page.locator('input[type=file]').count(),1);
 const upload=()=>page.locator('input[type=file]').setInputFiles({name:'test.txt',mimeType:'text/plain',buffer:Buffer.from('test data')});
 failUpload=true;await upload();await page.getByRole('alert').filter({hasText:'上传失败'}).waitFor();await upload();await page.locator('.file-row').waitFor();assert.equal(indexes[0].document_ids.length,1);
 await page.locator('.file-row').getByRole('button',{name:'移除',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'取消',exact:true}).click();assert.equal(indexes[0].document_ids.length,1);
 await page.locator('.file-row').getByRole('button',{name:'移除',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'移除',exact:true}).click();await page.locator('.file-row').waitFor({state:'hidden'});assert.equal(files.length,1,'unlink preserves pooled file');
 await upload();await page.locator('.file-row').waitFor();await page.locator('.file-row').getByRole('button',{name:'删除文件',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'确认删除',exact:true}).click();await page.locator('.file-row').waitFor({state:'hidden'});assert.equal(files.length,1);
 failDocs=true;await choice.selectOption('k2');await page.getByRole('alert').filter({hasText:'读取知识库文档失败'}).waitFor();await page.getByRole('alert').getByRole('button',{name:'重试',exact:true}).click();await page.getByRole('alert').waitFor({state:'hidden'});
 await page.getByRole('button',{name:'创建知识库',exact:true}).click();await page.getByLabel('知识库名称',{exact:true}).fill('新库');await page.getByLabel('知识库描述',{exact:true}).fill('新库描述');await page.getByRole('dialog').getByRole('button',{name:'保存',exact:true}).click();await page.getByRole('dialog').waitFor({state:'hidden'});assert.equal(await page.locator('input[type=file]').count(),1);
 await page.locator('.knowledge-actions').getByRole('button',{name:'删除',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'确认删除',exact:true}).click();await page.locator('input[type=file]').waitFor({state:'hidden'});
 // MCP and device-plugin create/edit/associate/delete, including parameter fields.
 await page.locator('summary').filter({hasText:'工具与插件'}).click();
 for(const kind of ['mcp','plugin']){
  const group=page.locator('.resource-list').filter({has:page.getByRole('heading',{name:kind==='mcp'?'MCP 工具':'设备插件',exact:true})});
  await group.getByRole('button',{name:kind==='mcp'?'+ 新建 MCP 工具':'+ 新建设备插件',exact:true}).click();
  const dialog=page.getByRole('dialog');await dialog.getByLabel('名称',{exact:true}).fill(kind+'新资源');await dialog.getByLabel('描述',{exact:true}).fill('描述');
  if(kind==='mcp'){
   await dialog.getByLabel('MCP 服务 URL').fill('https://example.test/tool');await dialog.getByLabel('认证方式').selectOption('bearer');await dialog.getByLabel('Bearer Token',{exact:true}).fill('synthetic-test-token');await dialog.getByLabel('启用',{exact:true}).uncheck();await dialog.getByLabel('启用',{exact:true}).check();
  }else{
   await dialog.getByLabel('功能标识（Action）').fill('demo_action');
   for(let i=0;i<2;i++){
    await dialog.getByRole('button',{name:'添加参数',exact:true}).nth(i).click();const card=dialog.locator('.param-card').nth(i);
    await card.getByLabel('参数名称',{exact:true}).fill(i?'result':'count');await card.getByRole('combobox').selectOption('integer');await card.getByLabel('参数说明',{exact:true}).fill('说明');await card.getByLabel('默认值',{exact:true}).fill('1');if(!i)await card.getByLabel('必填',{exact:true}).check();
   }
   await dialog.getByRole('button',{name:'添加参数',exact:true}).first().click();await dialog.locator('.param-card').nth(1).getByRole('button',{name:'删除参数',exact:true}).click();
  }
  await dialog.getByRole('button',{name:'保存',exact:true}).click();await dialog.waitFor({state:'hidden'});
  const checkbox=group.getByRole('checkbox',{name:kind+'新资源',exact:true});await checkbox.check();await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.waitForFunction(()=>document.querySelector('.save-bar .ant-btn-primary')?.disabled);const row=checkbox.locator('xpath=../..');
  await row.getByRole('button',{name:'编辑',exact:true}).click();await dialog.getByLabel('名称',{exact:true}).fill(kind+'已编辑');await dialog.getByRole('button',{name:'保存',exact:true}).click();await dialog.waitFor({state:'hidden'});
  const item=resources[kind].at(-1);assert.equal(item.name,kind+'已编辑');if(kind==='mcp')assert.equal(item.config.authentication.bearer_token,'synthetic-test-token');else {assert.equal(item.input_params.length,1);assert.equal(item.return_params.length,1);assert.equal(item.input_params[0].required,true);}
  await group.getByRole('checkbox',{name:kind+'已编辑',exact:true}).uncheck();
  await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.getByText('保存成功',{exact:true}).waitFor();
  const updated=group.getByRole('checkbox',{name:kind+'已编辑',exact:true}).locator('xpath=../..');await updated.getByRole('button',{name:'删除',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'确认删除',exact:true}).click();await group.getByRole('checkbox',{name:kind+'已编辑',exact:true}).waitFor({state:'hidden'});
 }
 for(const width of [390,1440]){await page.setViewportSize({width,height:1000});await choice.selectOption('k1');assert.equal(await page.locator('input[type=file]').count(),1);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.evaluate(()=>scrollTo(0,0));await page.waitForTimeout(100);await page.screenshot({path:root+'/../output/playwright/knowledge-verified-'+width+'.png',fullPage:true});}
 assert.deepEqual(errors,[]);console.log('PASS: knowledge create/edit/retry/delete; single upload section; upload failure/retry, unlink vs delete; documents retry; MCP and plugin CRUD/parameters/associations; desktop/mobile');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
