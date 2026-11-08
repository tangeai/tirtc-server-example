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
 let authWrites=[],badBind=true;
 await page.route('**/v1/config/captcha',route=>route.fulfill({json:{code:200,data:{enabled:false}}}));
 for(const endpoint of ['/v1/user/register','/v1/user/password-reset','/v1/user/login','/v1/user/send-code','/v1/user/password-reset/send-code'])await page.route('**'+endpoint,async route=>{authWrites.push({path:new URL(route.request().url()).pathname,body:route.request().postDataJSON()});await route.fulfill({json:{code:200,data:{token:'synthetic-browser-token'}}});});
 await page.route('**/v1/user/device/bind-by-id',async route=>{if(badBind){badBind=false;return route.fulfill({status:400,json:{code:4004,msg:'设备 ID 不存在或不在您的账号配额内'}});}await route.fulfill({json:{code:200,data:{device_id:'TG1234567890'}}});});

 await page.evaluate(()=>{localStorage.removeItem('token');localStorage.setItem('xiaotai.site.language','en');});
 for(const width of [320,390,768,1440,1920]){
  await page.setViewportSize({width,height:900});
  for(const url of ['/login','/register','/forgot-password']){
   await page.goto(base+url);await page.locator('form').waitFor();
   assert.equal(await page.locator('html').getAttribute('lang'),'en');
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   const text=await page.locator('main').innerText();assert(!/[\u3400-\u9fff]/.test(text),'untranslated auth UI: '+text);
   const email=page.locator('input[type=email]');await email.fill('draft@example.test');
   await page.getByRole('button',{name:'语言 / Language',exact:true}).click();await page.getByRole('menuitem',{name:'简体中文',exact:true}).click();assert.equal(await email.inputValue(),'draft@example.test');
   await page.getByRole('button',{name:'语言 / Language',exact:true}).click();await page.getByRole('menuitem',{name:'English',exact:true}).click();await page.locator('.ant-dropdown:visible').waitFor({state:'hidden'});assert.equal(await email.inputValue(),'draft@example.test');
   await page.screenshot({path:root+'/../output/playwright/auth-english-'+url.slice(1)+'-'+width+'.png'});
  }
 }
 await page.goto(base+'/register');await page.getByLabel('Email',{exact:true}).fill('synthetic@example.test');await page.getByLabel('Email verification code',{exact:true}).fill('123456');await page.getByLabel('Set password',{exact:true}).fill('testing123');await page.getByLabel('Confirm password',{exact:true}).fill('different123');await page.getByLabel('I have read and agree to').check();await page.getByRole('button',{name:'Sign up',exact:true}).click();await page.getByText('Passwords do not match',{exact:true}).waitFor();assert.equal(authWrites.length,0);
 await page.getByLabel('Confirm password',{exact:true}).fill('testing123');await page.getByRole('button',{name:'Sign up',exact:true}).click();await page.waitForURL('**/devices');
 await page.evaluate(()=>localStorage.removeItem('token'));await page.goto(base+'/forgot-password');await page.getByLabel('Registered email',{exact:true}).fill('synthetic@example.test');await page.getByRole('button',{name:'Get code',exact:true}).click();await page.getByText('If this email is registered, a verification code will be sent to it.',{exact:true}).waitFor();await page.getByLabel('Email verification code',{exact:true}).fill('123456');await page.getByLabel('New password',{exact:true}).fill('changed123');await page.getByLabel('Confirm new password',{exact:true}).fill('changed123');await page.getByRole('button',{name:'Reset password',exact:true}).click();await page.getByText('Password reset. Log in with your new password.',{exact:true}).waitFor();
 await page.goto(base+'/login');await page.getByLabel('Email',{exact:true}).fill('synthetic@example.test');await page.getByLabel('Password',{exact:true}).fill('changed123');await page.getByLabel('I have read and agree to').check();await page.getByRole('button',{name:'Log in',exact:true}).click();await page.waitForURL('**/devices');
 assert.deepEqual(errors,[]);console.log('PASS English auth: login/register/reset at 5 widths; no untranslated UI; language switching preserves email; mismatch validation; registration, reset-code/reset and login success');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
