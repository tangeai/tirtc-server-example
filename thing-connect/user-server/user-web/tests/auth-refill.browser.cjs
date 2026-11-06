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
 await email.fill('intended@example.test');await password.fill('intended-password');await page.getByLabel('我已阅读并同意').check();
 await page.getByRole('button',{name:'登录',exact:true}).last().click();await page.getByText('验证面板',{exact:true}).waitFor();
 // Native password managers can emit input after the challenge opens. This
 // must not substitute saved credentials or cancel the user's chosen account.
 await page.evaluate(()=>{for(const [selector,value] of [['input[type=email]','saved-old@example.test'],['input[type=password]','saved-old-password']]){const el=document.querySelector(selector);Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));}});
 assert.equal(await email.inputValue(),'intended@example.test','password-manager refill must not replace the submitted account');
 assert.equal(await password.inputValue(),'intended-password');assert.equal(await page.locator('.test-captcha').count(),1,'refill must not cancel verification');
 await page.evaluate(()=>window.finishChallenge());await page.getByRole('alert').waitFor();assert.equal(writes.length,1);assert.equal(writes[0].body.email,'intended@example.test');assert.equal(writes[0].body.password,'intended-password');
 assert.deepEqual(errors,[]);console.log('PASS: late password-manager refill preserves intended credentials and verification.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
