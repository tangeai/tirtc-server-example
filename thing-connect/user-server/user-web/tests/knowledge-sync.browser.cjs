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

 const kp='/v1/ai/knowledge/indexes';let uploads=0,reads=0,linked=false,failLink=false,oldIndexReads=0;
 await page.route('**/v1/ai/knowledge/**',async route=>{const req=route.request(),p=new URL(req.url()).pathname;let data={};
  if(p==='/v1/ai/knowledge/files'){uploads++;data={file_id:'file-'+uploads};}
  else if(p===kp)data={items:[{id:'k1',name:'测试库',description:'测试',document_ids:[]},{id:'k2',name:'其他库',description:'其他'}]};
  else if(p.endsWith('/documents')){reads++;oldIndexReads+=p.includes('k1')?1:0;data={items:linked&&reads>=3?[{document_id:'file-'+uploads,name:'upload.txt',size:4}]:[],total:linked&&reads>=3?1:0};}
  else if(req.method()==='PUT'){if(failLink){failLink=false;return route.fulfill({status:503,json:{code:50200,msg:'关联失败'}});}linked=true;}
  else data={id:'k1',name:'测试库',description:'测试',document_ids:[]};
  await route.fulfill({json:{code:200,data}});
 });
 await page.goto(base+'/agent?device_id=one');await page.getByRole('button',{name:'我的角色',exact:true}).click();await page.getByRole('radio',{name:'扩展能力',exact:true}).locator('..').click();await page.locator('summary').filter({hasText:'知识库'}).click();const choice=page.locator('.role-section select').filter({has:page.locator('option[value="k1"]')});await choice.selectOption('k1');
 const upload=()=>page.locator('input[type=file]').setInputFiles({name:'upload.txt',mimeType:'text/plain',buffer:Buffer.from('test')});
 await upload();await page.locator('.file-row').waitFor({timeout:7000});assert.equal(uploads,1);assert(reads>=3,'must confirm asynchronous document visibility');
 failLink=true;await upload();await page.getByRole('button',{name:'重试关联',exact:true}).waitFor();assert.equal(uploads,2);await page.getByRole('button',{name:'重试关联',exact:true}).click();await page.getByText('文件列表已更新',{exact:true}).waitFor();assert.equal(uploads,2,'retry association must not upload a duplicate');
 await page.getByRole('button',{name:'刷新文件列表',exact:true}).click();await page.getByRole('button',{name:'刷新文件列表',exact:true}).waitFor({state:'visible'});
 await choice.selectOption('k2');await page.waitForTimeout(100);const before=oldIndexReads;await page.waitForTimeout(1200);assert.equal(oldIndexReads,before,'old index must stop querying after switching');
 assert.deepEqual(errors,[]);console.log('PASS: delayed file visibility, association retry without duplicate upload, manual refresh and index switch');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
