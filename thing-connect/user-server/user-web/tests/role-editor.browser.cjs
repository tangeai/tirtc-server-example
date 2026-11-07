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
 const wav=Buffer.alloc(44+16000);wav.write('RIFF',0);wav.writeUInt32LE(wav.length-8,4);wav.write('WAVEfmt ',8);wav.writeUInt32LE(16,16);wav.writeUInt16LE(1,20);wav.writeUInt16LE(1,22);wav.writeUInt32LE(8000,24);wav.writeUInt32LE(16000,28);wav.writeUInt16LE(2,32);wav.writeUInt16LE(16,34);wav.write('data',36);wav.writeUInt32LE(16000,40);
 await page.route('**/preview.mp3',route=>route.fulfill({contentType:'audio/wav',body:wav}));
 await page.setViewportSize({width:1440,height:900});
 await page.goto(base+'/agent?device_id=one');await page.getByRole('button',{name:'我的角色',exact:true}).click();
 await page.getByLabel('智能体名称',{exact:true}).fill('新的名称');
 await page.getByText('更多角色信息',{exact:true}).click();
 await page.getByLabel('智能体描述',{exact:true}).fill('角色说明');await page.getByLabel('角色设定',{exact:true}).fill('测试设定');await page.getByLabel('开场白',{exact:true}).fill('你好');
 await page.getByText('语音设置',{exact:true}).click();await page.getByText('单独调整音色',{exact:true}).click();
 assert.equal(await page.getByLabel('声音列表',{exact:true}).locator('option').count(),3);
 await page.getByLabel('声音列表',{exact:true}).selectOption('zh');
 assert((await page.locator('audio').last().getAttribute('src')).endsWith('/preview.mp3'));
 await page.getByRole('button',{name:'试听',exact:true}).click();await page.getByRole('button',{name:'暂停',exact:true}).click();
 assert.equal(await page.getByLabel('角色设定',{exact:true}).inputValue(),'测试设定');
 const sliders=page.getByRole('slider');await sliders.nth(0).fill('1.5');await sliders.nth(1).fill('0.8');
 await page.getByRole('button',{name:'模型与记忆',exact:true}).click();
 await page.locator('.role-section select').first().selectOption('qwen3.7-plus');await page.getByLabel('长期记忆').check();await page.getByRole('button',{name:'角色',exact:true}).click();
 assert.equal(await page.getByLabel('智能体名称',{exact:true}).inputValue(),'新的名称');
 await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.getByText('保存成功',{exact:true}).waitFor();
 assert.equal(role.service_config.config.tts.provider_params.voice,'zh');assert.equal(role.agent_config.ali_memory.enable,true);assert.equal(role.agent_config.prompt,'测试设定');assert.equal(role.agent_config.welcome_text,'你好');assert.equal(role.user_params.description,'角色说明');assert.equal(role.service_config.config.tts.provider_params.rate,1.5);assert.equal(role.service_config.config.tts.provider_params.pitch,0.8);assert.equal(role.service_config.config.llm.provider_params.model,'qwen3.7-plus');
 for(const width of [320,390,768,1440]){
  await page.setViewportSize({width,height:900});
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  await page.screenshot({path:root+'/../output/playwright/role-voice-'+width+'.png',fullPage:true});
 }
 voiceMode='error';await page.getByRole('button',{name:'重新加载',exact:true}).click();await page.getByText('音色列表暂不可用，请稍后重试',{exact:false}).waitFor();
 voiceMode='empty';await page.getByRole('button',{name:'重试',exact:true}).click();await page.waitForFunction(()=>document.querySelector('select[aria-label="声音列表"]').options.length===2);
 voiceMode='ok';await page.getByRole('button',{name:'重新加载',exact:true}).click();await page.waitForFunction(()=>document.querySelector('select[aria-label="声音列表"] option[value="en"]'));
 assert((await page.locator('.voice-current').innerText()).includes('中文测试声音'));
 assert.deepEqual(errors,[]);console.log('PASS: voice catalog, unified persona and voice panel, draft preservation across sections, save payload, error/empty/retry, responsive layouts');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
