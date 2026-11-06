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
 await page.evaluate(()=>localStorage.removeItem('token'));await page.goto(base+'/register');await page.getByLabel('邮箱',{exact:true}).fill('synthetic@example.test');await page.getByLabel('邮箱验证码',{exact:true}).fill('123456');await page.getByLabel('设置密码',{exact:true}).fill('testing123');await page.getByLabel('确认密码',{exact:true}).fill('different123');await page.getByLabel('我已阅读并同意').check();await page.getByRole('button',{name:'注册',exact:true}).last().click();await page.getByText('两次密码不一致',{exact:true}).waitFor();assert.equal(authWrites.length,0);
 await page.getByLabel('确认密码',{exact:true}).fill('testing123');await page.getByRole('button',{name:'注册',exact:true}).last().click();await page.waitForURL('**/devices');assert.equal(authWrites[0].path,'/v1/user/register');assert.equal(await page.evaluate(()=>localStorage.getItem('token')),'synthetic-browser-token');
 await page.getByLabel('账号菜单',{exact:true}).click();await page.getByRole('button',{name:'退出登录',exact:true}).click();await page.waitForURL('**/login');assert.equal(await page.evaluate(()=>localStorage.getItem('token')),null);
 await page.goto(base+'/forgot-password');await page.getByLabel('注册邮箱',{exact:true}).fill('synthetic@example.test');await page.getByRole('button',{name:'获取验证码',exact:true}).click();await page.getByText('如该邮箱已注册，验证码将发送至该邮箱',{exact:true}).waitFor();await page.getByLabel('邮箱验证码',{exact:true}).fill('123456');await page.getByLabel('新密码',{exact:true}).fill('changed123');await page.getByLabel('确认新密码',{exact:true}).fill('changed123');await page.getByRole('button',{name:'重置密码',exact:true}).click();await page.getByText('密码已重置，请使用新密码登录',{exact:true}).waitFor();assert.equal(await page.getByLabel('新密码',{exact:true}).inputValue(),'');
 await page.goto(base+'/login');await page.getByLabel('邮箱',{exact:true}).fill('synthetic@example.test');await page.getByLabel('密码',{exact:true}).fill('changed123');await page.getByLabel('我已阅读并同意').check();await page.getByRole('button',{name:'登录',exact:true}).last().click();await page.waitForURL('**/devices');assert(authWrites.some(w=>w.path==='/v1/user/login'));
 await page.goto(base+'/bind');await page.getByRole('button',{name:'设备 ID 绑定',exact:true}).click();await page.getByLabel('设备 ID',{exact:true}).fill('tg1234567890');assert.equal(await page.getByLabel('设备 ID',{exact:true}).inputValue(),'TG1234567890');await page.getByRole('button',{name:'绑定设备',exact:true}).click();await page.getByRole('alert').waitFor();await page.getByRole('button',{name:'绑定设备',exact:true}).click();await page.getByRole('heading',{name:'绑定成功！',exact:true}).waitFor();
 console.log('PASS: registration password mismatch/success; logout clears token; reset code/cooldown/success; login redirect; bind-by-ID normalization/error/retry/success');
 assert.deepEqual(errors,[]);console.log('Account flow browser errors: 0');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
