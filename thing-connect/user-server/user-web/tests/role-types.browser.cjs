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
 else if(u.pathname==='/v1/ai/role-types')data={items:[{id:'one',name:'温柔伙伴',voice_id:'zh',voice_model:'cosyvoice-v3-flash',prompt:'温柔倾听，简洁回答。'},{id:'two',name:'活力伙伴',voice_id:'en',voice_model:'cosyvoice-v3-flash',prompt:'积极明快，一次只问一个问题。'},{id:'seed',name:'山行猴侠',voice_id:'seed-voice',voice_model:'seed-tts-2.0',prompt:'爽朗直接的同行伙伴。'}]};
 else if(u.pathname==='/v1/ai/roles'){if(req.method()==='POST'){role={...req.postDataJSON(),id:'created'};data=role;}else data={items:[role]};}
 else if(u.pathname==='/v1/ai/roles/default')data={id:'preset',name:'预设角色',agent_config:{prompt:'只读'}};
 else if(u.pathname==='/v1/ai/roles/custom'||u.pathname==='/v1/ai/roles/created'){if(req.method()==='PUT')role={...role,...req.postDataJSON()};if(req.method()==='DELETE')return route.fulfill({status:409,json:{code:40900,msg:'角色正在使用，请先解除设备关联',data:{devices:[{device_id:'two',device_name:'设备二'}]}}});data=role;}
 else if(u.pathname.includes('/v1/ai/device/')){if(req.method()==='PUT')bound=req.postDataJSON().role_id;data={role_id:bound==='preset'?'':bound,default_role_id:'preset'};}
 else if(u.pathname.includes('/v1/recognition/devices/'))data={policy:{enabled:false},schemes:[]};
 else if(u.pathname.includes('/v1/call/group/web/device/'))data={desired_state:'left',state:'left',online:true};
 else if(u.pathname==='/v1/ai/voices'){
 if(voiceMode==='error')return route.fulfill({status:503,json:{code:50000,msg:'声音暂不可用'}});
 data={items:voiceMode==='empty'?[]:[{id:'zh',name:'中文测试声音',languages:['zh-CN'],model:'cosyvoice-v3-flash',sample_url:'/preview.mp3'},{id:'en',name:'English Voice',languages:['en-US'],model:'cosyvoice-v3-flash'},{id:'seed-voice',name:'猴哥 2.0',languages:['zh'],model:'seed-tts-2.0'},{id:'other',name:'其他引擎',languages:['zh'],model:'other'}]};
 }
 else if(u.pathname.includes('/v1/ai/'))data={items:[]};
 await route.fulfill({json:{code:200,data}});
 });
 await page.goto(base+'/login');await page.evaluate(()=>localStorage.setItem('token','test'));
 const wav=Buffer.alloc(44+16000);wav.write('RIFF',0);wav.writeUInt32LE(wav.length-8,4);wav.write('WAVEfmt ',8);wav.writeUInt32LE(16,16);wav.writeUInt16LE(1,20);wav.writeUInt16LE(1,22);wav.writeUInt32LE(8000,24);wav.writeUInt32LE(16000,28);wav.writeUInt16LE(2,32);wav.writeUInt16LE(16,34);wav.write('data',36);wav.writeUInt32LE(16000,40);
 await page.route('**/preview.mp3',route=>route.fulfill({contentType:'audio/wav',body:wav}));

 await page.setViewportSize({width:1440,height:900});
 await page.goto(base+'/agent?device_id=one');
 await page.getByRole('button',{name:'＋ 新建角色',exact:true}).click();
 const prompt=page.getByLabel('角色设定',{exact:true});
 const choose=async name=>{await page.getByRole('button',{name:/角色类型/}).click();await page.getByRole('button',{name:new RegExp(name+' ')}).click();};
 await page.getByRole('button',{name:/角色类型/}).click();
 assert.equal(await page.getByLabel('类型语言',{exact:true}).count(),0);assert.equal(await page.getByRole('button',{name:'试听：山行猴侠',exact:true}).count(),0);await page.locator('.role-type-option').filter({hasText:'山行猴侠'}).getByText('暂无试听',{exact:true}).waitFor();
 const before=await prompt.inputValue();await page.getByRole('button',{name:'试听：温柔伙伴',exact:true}).click();assert.equal(await prompt.inputValue(),before);
 await page.getByRole('button',{name:/温柔伙伴 中文测试声音/}).click();
 // A new draft can inherit defaults from the system role; replacing is explicit.
 if(await page.getByRole('button',{name:'使用默认设定',exact:true}).isVisible())await page.getByRole('button',{name:'使用默认设定',exact:true}).click();
 assert.equal(await prompt.inputValue(),'温柔倾听，简洁回答。');
 await page.getByRole('switch',{name:'自定义提示词'}).check(); await prompt.fill('我的手动设定');await choose('活力伙伴');await page.getByRole('button',{name:'保留我的设定',exact:true}).click();assert.equal(await prompt.inputValue(),'我的手动设定');
 await choose('温柔伙伴');await page.getByRole('button',{name:'取消',exact:true}).click();assert.equal(await prompt.inputValue(),'我的手动设定');
 await choose('温柔伙伴');await page.getByRole('button',{name:'使用默认设定',exact:true}).click();assert.equal(await prompt.inputValue(),'温柔倾听，简洁回答。');
 await page.getByRole('switch',{name:'自定义提示词'}).check(); await prompt.fill('保存后的自定义设定');await page.getByLabel('智能体名称',{exact:true}).fill('新建联动角色');
 await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.getByText('保存成功',{exact:true}).waitFor();
 assert.equal(role.agent_config.prompt,'保存后的自定义设定');assert.equal(role.service_config.config.tts.provider_params.voice,'zh');assert(requests.some(r=>r.path==='/v1/ai/roles'&&r.method==='POST'));
 assert(!requests.some(r=>r.path==='/v1/ai/role-types'&&r.method!=='GET'));
 assert.equal(role.user_params.role_type_id,'one');
 await page.reload();await page.getByRole('button',{name:'新建联动角色',exact:true}).click();
 await page.getByRole('button',{name:'角色类型 温柔伙伴',exact:true}).waitFor();
 assert.equal(await prompt.inputValue(),'保存后的自定义设定');
 assert.equal(await page.getByRole('switch',{name:'自定义提示词'}).isChecked(),true);
 await choose('活力伙伴');await page.getByRole('button',{name:'使用默认设定',exact:true}).click();
 assert.equal(await prompt.inputValue(),'积极明快，一次只问一个问题。');assert.equal(await prompt.getAttribute('readonly'),'');
 await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.getByText('保存成功',{exact:true}).waitFor();
 assert(requests.some(r=>r.path==='/v1/ai/roles/created'&&r.method==='PUT'));
 assert.equal(role.user_params.role_type_id,'two');assert.equal(role.service_config.config.tts.provider_params.voice,'en');

 await choose('山行猴侠');
 assert.equal(await prompt.inputValue(),'爽朗直接的同行伙伴。');
 await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.getByText('保存成功',{exact:true}).waitFor();
 assert.deepEqual(role.service_config.config.tts,{provider:'ve-seed-tts-2',provider_params:{model:'seed-tts-2.0',audio:{voice_type:'seed-voice',speech_rate:0,pitch_rate:0}}});
 await page.reload();await page.getByRole('button',{name:'新建联动角色',exact:true}).click();
 await page.getByRole('button',{name:'角色类型 山行猴侠',exact:true}).waitFor();
 assert.equal(await prompt.inputValue(),'爽朗直接的同行伙伴。');
 await page.getByText('语音设置',{exact:true}).click();
 assert.equal(await page.getByText('单独调整音色',{exact:true}).count(),0);assert.equal(await page.getByLabel('声音列表',{exact:true}).count(),0);
 const sliders=page.getByRole('slider');assert.equal(await sliders.nth(0).inputValue(),'1');assert.equal(await sliders.nth(1).inputValue(),'1');assert.equal(await sliders.nth(0).getAttribute('aria-valuetext'),'标准');
 await sliders.nth(0).fill('2');await sliders.nth(1).fill('0');await page.getByRole('button',{name:'保存角色',exact:true}).click();await page.getByText('保存成功',{exact:true}).waitFor();assert.equal(role.service_config.config.tts.provider_params.audio.speech_rate,20);assert.equal(role.service_config.config.tts.provider_params.audio.pitch_rate,-2);
 for(const width of [320,390,768,1440]){await page.setViewportSize({width,height:900});await page.getByRole('button',{name:/角色类型/}).click();assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:root+'/../output/playwright/role-types-'+width+'.png',fullPage:true});await page.keyboard.press('Escape');}
 await page.getByRole('switch',{name:'自定义提示词'}).check();await prompt.fill('这是较长的角色设定，用于检查阅读区域。\n'.repeat(80));
 for(const viewport of [{width:1440,height:900},{width:1366,height:768},{width:390,height:844}]){
  await page.setViewportSize(viewport);
  const bounds=await page.evaluate(()=>({page:document.documentElement.scrollHeight,height:innerHeight,prompt:document.querySelector('.role-prompt').getBoundingClientRect().height,save:document.querySelector('.save-bar').getBoundingClientRect().bottom,speech:document.querySelector('.role-speech').getBoundingClientRect().bottom}));
  assert(await prompt.evaluate(el=>el.scrollHeight>el.clientHeight),'long prompt scrolls inside the textarea');
  assert(bounds.page<=bounds.height,'role page must fit viewport: '+JSON.stringify(bounds));assert(bounds.prompt>=60,'prompt remains readable: '+JSON.stringify(bounds));assert(bounds.save<=bounds.height&&bounds.speech<=bounds.height,'controls remain visible');
  await page.screenshot({path:root+'/../output/playwright/role-fit-'+viewport.width+'.png',fullPage:true});
 }
 assert.deepEqual(errors,[]);console.log('PASS role type preview, voice/prompt selection, keep/replace/cancel, POST snapshot, responsive layouts');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
