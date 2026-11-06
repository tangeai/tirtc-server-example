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

 await page.route('**/v1/**', route=>{const pathname=new URL(route.request().url()).pathname;if(pathname.endsWith('/language.js'))return route.continue();return route.fulfill({json:{code:200,data:pathname==='/v1/user/device/list'?[]:{}}});});
 await page.route('**/v1/config/captcha',route=>route.fulfill({json:{code:200,data:{enabled:true,provider:'yidun',captcha_id:'test'}}}));
 let logins=[], allowLogin=false;
 await page.route('**/v1/user/login',route=>{logins.push(route.request().postDataJSON().email);return allowLogin?route.fulfill({json:{code:200,data:{token:'synthetic-'+logins.at(-1)}}}):route.fulfill({status:401,json:{code:401,msg:'邮箱或密码错误'}});});
 await page.route('https://cstaticdun.126.net/load.min.js',route=>route.fulfill({contentType:'text/javascript',body:`
 window.captchas=[];
 window.initNECaptcha=(options,ready)=>{
  const popup=document.createElement('div');popup.className='yidun_popup';popup.style.display='none';
  popup.innerHTML='<div class="yidun_popup__mask" style="background:#8888"></div><button style="position:relative" type="button">验证面板</button>';
  document.querySelector(options.element).append(popup);
  const state={options,popup,destroyed:false,complete(){options.onVerify(null,{validate:'synthetic-ticket'});}};window.captchas.push(state);
  ready({verify(){popup.style.display='block'},refresh(){},destroy(){state.destroyed=true;popup.remove()}});
 };
 `}));
 await page.setViewportSize({width:390,height:844});await page.goto(base+'/login');
 await page.getByLabel('邮箱',{exact:true}).fill('first@example.test');await page.getByLabel('密码',{exact:true}).fill('synthetic-password');await page.getByLabel('我已阅读并同意').check();
 const login=page.getByRole('button',{name:'登录',exact:true}).last();await login.click();await page.getByRole('button',{name:'验证面板',exact:true}).waitFor();
 // Safari's persisted pageshow restores the same JS heap and SDK overlay.
 // Emulate that lifecycle boundary without depending on browser cache heuristics.
 await page.evaluate(()=>{dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}));dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));});
 assert.equal(await page.locator('.yidun_popup').count(),0,'restored login page must not retain an inactive captcha overlay');
 await page.getByLabel('邮箱',{exact:true}).fill('second@example.test');await login.click();await page.getByRole('button',{name:'验证面板',exact:true}).waitFor();
 await page.evaluate(()=>window.captchas[0].complete());await page.waitForTimeout(50);assert.deepEqual(logins,[],'old challenge must not submit the new account');
 await page.evaluate(()=>window.captchas.at(-1).complete());await page.getByRole('alert').filter({hasText:'邮箱或密码错误'}).waitFor();
 assert.deepEqual(logins,['second@example.test']);assert.equal(await page.locator('.yidun_popup').count(),0,'completed verification must release its overlay even when login fails');
 await login.click();await page.getByRole('button',{name:'验证面板',exact:true}).waitFor();assert.equal(await page.evaluate(()=>window.captchas.length),3,'retry must create a fresh challenge');
 await page.getByLabel('邮箱',{exact:true}).fill('third@example.test');await login.click();await page.getByRole('button',{name:'验证面板',exact:true}).waitFor();
 await page.evaluate(()=>window.captchas[2].complete());await page.waitForTimeout(50);assert.deepEqual(logins,['second@example.test']);
 await page.evaluate(()=>window.captchas.at(-1).complete());await page.getByRole('alert').filter({hasText:'邮箱或密码错误'}).waitFor();assert.deepEqual(logins,['second@example.test','third@example.test']);
 // Exercise the complete success -> logout -> different account flow with
 // verification enabled (the general account-flow suite disables CAPTCHA).
 allowLogin=true;
 for(const email of ['account-a@example.test','account-b@example.test','account-c@example.test']) {
   await page.getByLabel('邮箱',{exact:true}).fill(email);
   await page.getByLabel('密码',{exact:true}).fill('synthetic-password');
   await page.getByLabel('我已阅读并同意').check();
   await login.click();await page.getByRole('button',{name:'验证面板',exact:true}).waitFor();
   await page.evaluate(()=>window.captchas.at(-1).complete());await page.waitForURL('**/devices');
   assert.equal(await page.evaluate(()=>localStorage.getItem('token')),'synthetic-'+email);
   await page.getByLabel('账号菜单',{exact:true}).click();await page.getByRole('button',{name:'退出登录',exact:true}).click();await page.waitForURL('**/login');
   assert.equal(await page.evaluate(()=>localStorage.getItem('token')),null);
   assert.equal(await page.locator('.yidun_popup').count(),0);
 }
 assert.deepEqual(errors,[]);console.log('PASS: restored page releases overlay; late challenge cannot submit another account; completed and failed login allow a fresh retry; three verified login/logout/account-switch cycles.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
