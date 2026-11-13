const {chromium,webkit}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs=require('fs'),path=require('path'),http=require('http'),assert=require('assert/strict');
const root=path.resolve(__dirname,'../../..');
fs.mkdirSync(path.join(root,'../output/playwright'),{recursive:true});
const requests=[],errors=[];
const server=http.createServer((req,res)=>{const url=new URL(req.url,'http://localhost');let file;
 if(url.pathname==='/v1/config/language.js'){res.setHeader('Content-Type','text/javascript');res.end('window.WebLanguageConfig={default:"en",allow_switch:true};'+fs.readFileSync(root+'/internal/webui/runtime.js','utf8')+';WebI18n.addMessages('+fs.readFileSync(root+'/internal/webui/common.json','utf8')+');');return;}
 file=url.pathname.startsWith('/static/')?root+'/user-server'+url.pathname:root+'/user-server/static/app/index.html';
 if(!fs.existsSync(file)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base='http://127.0.0.1:'+server.address().port;
 const browser=process.env.PLAYWRIGHT_BROWSER==='webkit'?await webkit.launch({headless:true}):await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM || undefined,args:['--no-sandbox']});
 try{
 for(const provider of ['yidun','geetest','tencent','aliyun']) {
 const context=await browser.newContext({locale:'zh-CN'});const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));

 const writes=[];
 await page.route('**/v1/**', async route=>{const p=new URL(route.request().url()).pathname;if(p.endsWith('/language.js'))return route.continue();if(route.request().method()==='POST')writes.push(p);return route.fulfill({json:{code:200,data:p==='/v1/config/captcha'?{enabled:true,provider,captcha_id:'fixture',public_config:{prefix:'fixture'}}:{}}});});
 await page.route(/^https:\/\/(cstaticdun\.126\.net|static\.geetest\.com|turing\.captcha\.qcloud\.com|o\.alicdn\.com)\//,route=>route.fulfill({contentType:'text/javascript',body:''}));
 await page.addInitScript(()=>{
  window.captchaOptions=[];
  const instance={verify(){},refresh(){},show(){},reset(){},showCaptcha(){},destroy(){window.destroyed=(window.destroyed||0)+1;}};
  window.initNECaptcha=(options,ready)=>{window.captchaOptions.push(options.lang);window.oldVerify=()=>options.onVerify(null,{validate:'fixture-only'});ready(instance);};
  window.initGeetest4=(options,ready)=>{window.captchaOptions.push(options.language);ready({...instance,onSuccess(fn){window.oldVerify=fn;},getValidate(){return {lot_number:'fixture-only'};}});};
  window.TencentCaptcha=function(id,callback,options){window.captchaOptions.push(options.userLanguage);window.oldVerify=()=>callback({ret:0,ticket:'fixture-only'});return instance;};
  window.initAliyunCaptcha=options=>{window.captchaOptions.push(options.language);window.oldVerify=()=>options.success('fixture-only');options.getInstance(instance);};
 });
 await page.goto(base+'/login');await page.locator('input[type=email]').fill('fixture@example.test');await page.locator('input[type=password]').fill('Test12345');await page.getByRole('checkbox').check();await page.getByRole('button',{name:'Log in',exact:true}).click();
 await page.waitForFunction(()=>window.captchaOptions.length===1);
 assert.deepEqual(await page.evaluate(()=>window.captchaOptions),[provider==='geetest'?'eng':'en'],provider+' must follow the English site even with a Chinese browser');
 await page.evaluate(()=>WebI18n.setLanguage('zh-CN'));
 await page.waitForFunction(()=>window.destroyed===1);
 await page.evaluate(()=>window.oldVerify());assert.equal(writes.length,0,'old-language token must not submit credentials');
 await page.getByRole('button',{name:'登录',exact:true}).click();await page.waitForFunction(()=>window.captchaOptions.length===2);
 assert.deepEqual(await page.evaluate(()=>window.captchaOptions),[provider==='geetest'?'eng':'en',({yidun:'zh-CN',geetest:'zho',tencent:'zh-cn',aliyun:'cn'})[provider]]);
 assert.deepEqual(errors,[]);console.log('PASS',provider,'follows site language and cancels old-language attempts');
 await context.close();
 }
 }finally{await browser.close();server.close();}
})().catch(error=>{console.error(error);server.close();process.exitCode=1;});
