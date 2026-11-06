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

 await page.route('**/v1/config/captcha',route=>route.fulfill({json:{code:200,data:{enabled:true,provider:'yidun',captcha_id:'test'}}}));
 await page.route('**/v1/user/**',route=>{requests.push(route.request().url());return route.fulfill({json:{code:200,data:{}}});});
 await page.route('https://cstaticdun.126.net/load.min.js',route=>route.fulfill({contentType:'text/javascript',body:`
 window.initNECaptcha=(options,ready)=>{
  const popup=document.createElement('div');popup.className='yidun_popup';popup.style.display='none';
  const mask=document.createElement('div');mask.className='yidun_popup__mask';mask.style.background='rgba(0,0,0,.45)';popup.append(mask);
  const panel=document.createElement('div');panel.className='fixture-captcha-panel';panel.style.cssText='position:absolute;left:50%;top:50%;transform:translate(-50%,-50%);width:'+options.width+';height:280px;background:white;padding:16px;border-radius:12px;box-sizing:border-box';
  panel.innerHTML='<p>安全验证（测试面板）</p><button type="button" aria-label="关闭安全验证">×</button><div style="height:150px;background:#e4efeb;margin-top:16px">验证内容</div>';
  popup.append(panel);document.querySelector(options.element).append(popup);
  const close=()=>{popup.style.display='none';options.onClose?.();};panel.querySelector('button').onclick=close;mask.onclick=close;
  ready({verify(){popup.style.display='block';},refresh(){},destroy(){popup.remove();}});
 };
 `}));
 for(const width of [320,390,768,1440]){
  await page.setViewportSize({width,height:800});
  for(const url of ['/login','/register','/forgot-password']){
   await page.goto(base+url);await page.getByLabel(url==='/forgot-password'?'注册邮箱':'邮箱',{exact:true}).fill('layout@example.test');
   if(url!=='/forgot-password')await page.getByLabel('我已阅读并同意').check();
   if(url==='/login')await page.getByLabel('密码',{exact:true}).fill('test123456');
   const before=await page.evaluate(()=>({height:document.documentElement.scrollHeight,scroll:scrollY}));
   const action=url==='/login'?page.getByRole('button',{name:'登录',exact:true}).last():page.getByRole('button',{name:'获取验证码',exact:true});
   await action.click();await page.getByLabel('关闭安全验证').waitFor();
   const position=await page.locator('.yidun_popup').evaluate(e=>({position:getComputedStyle(e).position,rect:{x:e.getBoundingClientRect().x,y:e.getBoundingClientRect().y,width:e.getBoundingClientRect().width,height:e.getBoundingClientRect().height},height:document.documentElement.scrollHeight}));
   assert.equal(position.position,'fixed');assert.equal(position.rect.y,0);assert.equal(position.rect.width,width);assert.equal(position.rect.height,800);assert.equal(position.height,before.height,'captcha must not extend document');
   const panel=await page.locator('.fixture-captcha-panel').boundingBox();assert(panel.x>=0&&panel.y>=0&&panel.x+panel.width<=width&&panel.y+panel.height<=800);
   assert(await page.evaluate(()=>document.elementFromPoint(2,100)?.classList.contains('yidun_popup__mask')),'mask must intercept background');
   await page.screenshot({path:root+'/../output/playwright/captcha-'+url.slice(1)+'-'+width+'.png'});
   await page.getByLabel('关闭安全验证').click();await page.waitForTimeout(30);assert.equal(await page.getByText('请完成人机验证',{exact:true}).count(),0);
   await action.click();await page.getByLabel('关闭安全验证').waitFor();await page.getByLabel('关闭安全验证').click();
  }
 }
 assert.deepEqual(requests,[],'closing verification must not submit');assert.deepEqual(errors,[]);console.log('PASS: captcha fixed overlay, centered panel, background interception, close/reopen, no premature submission; login/register/reset at 4 widths.');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
