const {chromium,webkit}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs=require('fs'),path=require('path'),http=require('http'),assert=require('assert/strict');
const root=path.resolve(__dirname,'../../..');
fs.mkdirSync(path.join(root,'../output/playwright'),{recursive:true});
const requests=[],errors=[];
const server=http.createServer((req,res)=>{const url=new URL(req.url,'http://localhost');let file;
 if(url.pathname==='/v1/config/language.js'){res.setHeader('Content-Type','text/javascript');res.end('window.WebLanguageConfig={default:"zh-CN",allow_switch:true};'+fs.readFileSync(root+'/internal/webui/runtime.js','utf8')+';WebI18n.addMessages('+fs.readFileSync(root+'/internal/webui/common.json','utf8')+');');return;}
 file=url.pathname.startsWith('/static/')?root+'/user-server'+url.pathname:root+'/user-server/static/app/index.html';
 if(!fs.existsSync(file)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base='http://127.0.0.1:'+server.address().port;
 const browser=process.env.PLAYWRIGHT_BROWSER==='webkit'?await webkit.launch({headless:true}):await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM || undefined,args:['--no-sandbox']});
 try{
 const context=await browser.newContext();const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));

 const writes=[];let releaseLogin,loginResponse;
 await page.route('**/v1/**',async route=>{const u=new URL(route.request().url());if(u.pathname.endsWith('/language.js'))return route.continue();let data={};if(u.pathname==='/v1/config/captcha')data={enabled:true,provider:'yidun',captcha_id:'test'};else if(route.request().method()==='POST'){writes.push({path:u.pathname,body:route.request().postDataJSON()});if(loginResponse&&u.pathname==='/v1/user/login'){await loginResponse;return route.fulfill({json:{code:200,data:{token:'synthetic-old-result'}}});}return route.fulfill({status:400,json:{code:4000,msg:'测试错误，请重试'}});}return route.fulfill({json:{code:200,data}});});
 await page.route('https://cstaticdun.126.net/load.min.js',route=>route.fulfill({contentType:'text/javascript',body:`
 window.initNECaptcha=(options,ready)=>{const el=document.createElement('div');el.textContent='验证面板';el.className='test-captcha';document.querySelector(options.element).append(el);window.finishChallenge=()=>options.onVerify(null,{validate:'synthetic-only'});ready({verify(){},refresh(){},destroy(){el.remove()}});};
 `}));
 await page.setViewportSize({width:390,height:844});await page.goto(base+'/login');
 const email=page.getByLabel('邮箱',{exact:true}), password=page.getByLabel('密码',{exact:true});
 await email.fill('old@example.test');await password.fill('old-password');await page.getByLabel('我已阅读并同意').check();
 // Browser autofill/restoration can update native values without React change
 // events. The account on screen must remain the account submitted after CAPTCHA.
 await email.evaluate(el=>Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,'new@example.test'));
 await password.evaluate(el=>Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,'new-password'));
 await page.getByRole('button',{name:'登录',exact:true}).last().click();await page.getByText('验证面板',{exact:true}).waitFor();
 assert.equal(await email.inputValue(),'new@example.test','opening CAPTCHA must not replace the displayed account with stale React state');
 assert.equal(await password.inputValue(),'new-password');await page.evaluate(()=>window.finishChallenge());await page.getByRole('alert').waitFor();
 assert.equal(writes.length,1);assert.equal(writes[0].body.email,'new@example.test');assert.equal(writes[0].body.password,'new-password');
 // A silent browser fill during verification must not change the identity
 // attached to that attempt or allow its old ticket to authenticate another user.
 await page.getByRole('button',{name:'登录',exact:true}).last().click();await page.getByText('验证面板',{exact:true}).waitFor();
 await email.evaluate(el=>Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,'changed@example.test'));
 await page.evaluate(()=>window.finishChallenge());await page.getByRole('alert').filter({hasText:'表单内容已变化'}).waitFor();assert.equal(writes.length,1);
 // Silent changes while the request is in flight cannot commit its token.
 loginResponse=new Promise(resolve=>releaseLogin=resolve);
 await page.getByRole('button',{name:'登录',exact:true}).last().click();await page.getByText('验证面板',{exact:true}).waitFor();await page.evaluate(()=>window.finishChallenge());
 await page.waitForFunction(()=>document.querySelector('fieldset').disabled);
 await email.evaluate(el=>Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,'latest@example.test'));
 releaseLogin();await page.getByRole('alert').filter({hasText:'表单内容已变化'}).waitFor();assert.equal(await page.evaluate(()=>localStorage.getItem('token')),null);assert(new URL(page.url()).pathname==='/login');loginResponse=undefined;
 // Retrying is still usable after cancelling the obsolete result.
 await page.getByRole('button',{name:'登录',exact:true}).last().click();await page.getByText('验证面板',{exact:true}).waitFor();await page.evaluate(()=>window.finishChallenge());await page.getByRole('alert').filter({hasText:'测试错误'}).waitFor();assert.equal(writes.at(-1).body.email,'latest@example.test');
 // Registration and password reset read native values too, including the OTP
 // and confirmation password. Status/consent renders must preserve autofill.
 for(const url of ['/register','/forgot-password']) {
   await page.goto(base+url);if(url==='/register')await page.getByLabel('我已阅读并同意').check();
   await page.evaluate(()=>{for(const [name,value] of Object.entries({email:'native@example.test',password:'native-password',confirm:'native-password',code:'123456'})){const el=document.querySelector('form').elements.namedItem(name);Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,value);}});
   await page.getByRole('button',{name:url==='/register'?'注册':'重置密码',exact:true}).last().click();await page.getByRole('alert').filter({hasText:'测试错误'}).waitFor();
   assert.equal(writes.at(-1).body.email,'native@example.test');assert.equal(writes.at(-1).body.password,'native-password');assert.equal(writes.at(-1).body.code,'123456');
 }
 assert.deepEqual(errors,[]);console.log('PASS: native autofill survives rendering; submitted identity matches screen; verification/request-time changes cannot authenticate; retry, registration and reset remain usable.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
