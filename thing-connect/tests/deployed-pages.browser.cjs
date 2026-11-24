// Read-only smoke acceptance against an already deployed Nginx entry.
// No API interception, synthetic JWT, account mutation, mail or cloud calls.
const {chromium}=require('../user-server/user-web/node_modules/playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
const base=process.env.ACCEPTANCE_BASE_URL||'http://127.0.0.1:8080';
const browserURL=new URL(base);if(process.env.ACCEPTANCE_HOST)browserURL.hostname=process.env.ACCEPTANCE_HOST;
const browserBase=browserURL.origin;const headers=process.env.ACCEPTANCE_HOST?{Host:process.env.ACCEPTANCE_HOST}:{};
const out=path.resolve(__dirname,'../../output/playwright/deployed-pages');fs.mkdirSync(out,{recursive:true});
(async()=>{
 const browser=await chromium.launch({headless:true,args:['--no-sandbox',...(process.env.ACCEPTANCE_HOST?['--host-resolver-rules=MAP '+process.env.ACCEPTANCE_HOST+' 127.0.0.1']:[])]});const results=[];let successful=false;
 const context=await browser.newContext();
 const publicPaths=['/','/boards','/getting-started','/faq','/guides/simulator','/guides/wechat-voip','/guides/linux-device','/login','/register','/forgot-password','/static/legal/terms.html','/static/legal/privacy.html','/admin/'];
 try{
  const page=await context.newPage();let errors=[],failedAssets=[];
  page.on('pageerror',e=>errors.push(e.message));
  page.on('response',r=>{if(['script','stylesheet'].includes(r.request().resourceType())&&r.status()>=400)failedAssets.push(new URL(r.url()).pathname+':'+r.status());});
  for(const width of [1440,390])for(const route of publicPaths){
   errors=[];failedAssets=[];await page.setViewportSize({width,height:900});
   const r=await page.goto(browserBase+route,{waitUntil:'domcontentloaded'});
   assert(r&&r.ok(),route+' HTTP status');
   await page.waitForFunction(()=>document.body.innerText.trim().length>20);
   if(route==='/admin/') await page.getByLabel('管理员邮箱',{exact:true}).waitFor({timeout:20000});
   const text=await page.locator('body').innerText();assert(text.trim().length>20,route+' empty page');
   assert.deepEqual(errors,[],route+' runtime errors');assert.deepEqual(failedAssets,[],route+' static assets');
   const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth+2);assert(!overflow,route+' horizontal page overflow');
   await page.screenshot({path:path.join(out,(route.replaceAll('/','_')||'home')+'-'+width+'.png'),fullPage:true});
   results.push({route,width,status:'pass'});console.log('PASS',route,width);
  }
  for(const route of ['/devices','/bind','/contacts','/agent','/room','/player']){
   await page.goto(browserBase+route);await page.waitForURL('**/login');
   results.push({route,scenario:'anonymous-redirect',status:'pass'});
  }
  for(const route of ['/v1/user/me','/v1/call/room','/v1/ai/token','/v1/admin/me']){
   const response=await context.request.get(base+route,{headers});assert.equal(response.status(),401,route);
   results.push({route,scenario:'anonymous-denied',status:'pass'});
  }
  for(const route of ['/v1/internal/test','/v1/ai/internal/unbind','/v1/voip/internal/unbind','/v1/call/internal/unbind']){
   const response=await context.request.post(base+route,{data:{},headers});assert.equal(response.status(),404,route);
   results.push({route,scenario:'internal-not-public',status:'pass'});
  }
  successful=true;console.log('PASS real deployment: public pages, protected-page redirects, authentication and internal-route isolation');
 }finally{fs.writeFileSync(path.join(out,'results.json'),JSON.stringify({at:new Date().toISOString(),base,browserBase,status:successful?'pass':'fail',mode:'real-nginx-no-mocks',results,notCovered:['authenticated business mutations','real device/media','external mail, AI, VoIP and recognition']},null,2));await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
