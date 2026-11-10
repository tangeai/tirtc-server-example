const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs=require('fs'),path=require('path'),http=require('http'),assert=require('assert/strict');
const root=path.resolve(__dirname,'../../..');
fs.mkdirSync(path.join(root,'../output/playwright'),{recursive:true});
const requests=[],errors=[];
const server=http.createServer((req,res)=>{const url=new URL(req.url,'http://localhost');let file;
 if(url.pathname==='/v1/config/language.js'){res.setHeader('Content-Type','text/javascript');res.end('window.WebLanguageConfig={default:"zh-CN",allow_switch:true};'+fs.readFileSync(root+'/internal/webui/runtime.js','utf8')+';WebI18n.addMessages('+fs.readFileSync(root+'/internal/webui/common.json','utf8')+');');return;}
 file=url.pathname.startsWith('/static/')?root+'/user-server'+url.pathname:url.pathname==='/'?root+'/user-server/static/index.html':url.pathname==='/boards'?root+'/user-server/static/boards.html':root+'/user-server/static/app/index.html';
 if(!fs.existsSync(file)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM,args:['--no-sandbox']});
 try {
 const page=await browser.newPage({reducedMotion:'reduce'});page.on('pageerror',e=>errors.push(e.message));
 let failed=false;
 await page.route('**/v1/config/site',route=>route.fulfill({json:{code:200,data:{name:'自定义站点',name_en:'Custom site'}}}));
 const sampleBoard={name:'小钛开发板完整型号与名称检查',model:'ESP32-S3-DISPLAY-LONG-MODEL-NUMBER',vendor:'测试厂商',chip:'ESP32-S3',adaptation_status:'ready',summary:'用于浏览器交互验证的测试开发板。',detail_slug:'sample',capabilities:['AI 对话','实时音视频','设备对讲'],firmware_url:'https://example.test/firmware',flashing_guide_url:'https://example.test/guide'};
 await page.route('**/v1/boards',route=>{const home=new URL(route.request().headers().referer||base).pathname==='/';const boards=home?Array.from({length:4},(_,i)=>({...sampleBoard,name:sampleBoard.name+' '+(i+1),detail_slug:'sample-'+i})): [sampleBoard];return route.fulfill({json:failed?{code:50000,msg:'目录加载失败'}:{code:200,data:{boards}}});});
 for(const width of [320,390,768,1024,1280,1440,1920]){
  await page.setViewportSize({width,height:900});
  for(const url of ['/','/boards']){
   await page.goto(base+url);await page.waitForFunction(()=>window.SiteI18n);
   if(url==='/boards')await page.locator('.featured-card').waitFor();else await page.locator('.catalog-card').first().waitFor();
   for(const language of ['zh-CN','en']){
    if(await page.locator('html').getAttribute('lang')!==language)await page.locator('[data-site-language-toggle]').click();
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),url+' overflow '+width+' '+language+' '+JSON.stringify(await page.evaluate(()=>[...document.querySelectorAll('body *')].map(e=>({tag:e.tagName,cls:e.className,x:e.getBoundingClientRect().x,right:e.getBoundingClientRect().right})).filter(e=>e.right>innerWidth+1||e.x< -1).slice(0,12))));
    if(width<=768){const menu=page.locator('.menu-button');const menuBox=await menu.boundingBox();assert(menuBox.width>=43.99&&menuBox.height>=43.99,JSON.stringify({url,width,language,menuBox}));await menu.click();await page.keyboard.press('Escape');assert.equal(await menu.getAttribute('aria-expanded'),'false');assert(await menu.evaluate(e=>e===document.activeElement));await menu.click();await page.mouse.click(2,850);assert.equal(await menu.getAttribute('aria-expanded'),'false');}
    if(url==='/'){
     assert.equal(await page.locator('.catalog-card').count(),3,'homepage shows three boards from a larger catalog');
     const navBox=await page.locator('.site-header .nav').boundingBox(),heroBox=await page.locator('.hero').boundingBox();
     const footerBox=await page.locator('.footer-contact').boundingBox();assert(Math.abs(footerBox.x-heroBox.x)<1&&Math.abs(footerBox.width-heroBox.width)<1,'footer aligns with content');
     assert(Math.abs(navBox.x-heroBox.x)<1&&Math.abs(navBox.width-heroBox.width)<1,'homepage header and content must align');
     const boardBox=await page.locator('.showcase .board').boundingBox(),captionBox=await page.locator('.device-caption').boundingBox();
     const showcaseBox=await page.locator('.showcase').boundingBox();
     assert(boardBox.x>=showcaseBox.x-1&&boardBox.x+boardBox.width<=showcaseBox.x+showcaseBox.width+1,'board must not be clipped '+width+' '+language);
     assert(captionBox.y>=boardBox.y+boardBox.height+6,'board caption overlaps board '+width+' '+language+' '+JSON.stringify({boardBox,captionBox}));
     assert.equal(await page.locator('.feature-tabs button>svg').count(),4);
     if(language==='zh-CN')assert(!(await page.locator('h1').innerText()).endsWith('。'));
     assert.equal(await page.locator('a[href^="mailto:"]').getAttribute('href'),'mailto:'+await page.locator('a[href^="mailto:"]').innerText());
     await page.locator('#simulator-details').evaluate(e=>e.open=true);assert.equal(await page.locator('.catalog-actions .guide-link').first().getAttribute('href'),'https://example.test/guide'); assert.equal(await page.locator('dialog').count(),0);const tabs=page.locator('[data-sim-tab]');await tabs.first().focus();await page.keyboard.press('ArrowRight');assert.equal(await tabs.nth(1).getAttribute('aria-selected'),'true');
    }else{
     assert.match(await page.locator('.board-capabilities').innerText(),/实时音视频/);
     await page.locator('.featured-card .detail').click();await page.getByRole('dialog',{name:'小钛开发板完整型号与名称检查'}).waitFor();
     assert(await page.locator('#detail-guide').isVisible());assert(await page.locator('#detail-buy').isHidden());
     const box=await page.locator('#detail-dialog').boundingBox();assert(box.x>=0&&box.x+box.width<=width);
     await page.keyboard.press('Escape');assert(await page.locator('#detail-dialog').isHidden());
    }
    await page.evaluate(()=>scrollTo({top:0,behavior:'instant'}));await page.waitForTimeout(250);
    if(url==='/'&&language==='zh-CN'&&(width===390||width===1440)){await page.screenshot({path:root+'/../output/playwright/home-full-'+width+'.png',fullPage:true});}
    await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:root+'/../output/playwright/public-review-'+(url==='/'?'home':'boards')+'-'+language+'-'+width+'.png'});
   }
  }
 }
 await page.setViewportSize({width:1440,height:1000});
 await page.goto(base+'/');await page.locator('.catalog-card').first().waitFor();
 assert.equal(await page.locator('.brand [data-i18n="site.brand"]').innerText(),'Custom site');
 for(let scene=0;scene<4;scene++){
  await page.locator('[data-feature="'+scene+'"]').click();
  assert.equal(await page.locator('.showcase .screen').getAttribute('data-scene'),String(scene));
  assert.equal(await page.locator('[data-feature="'+scene+'"]').getAttribute('aria-pressed'),'true');
  assert(await page.locator('#screen-title').innerText());
 }
 assert(await page.locator('.camera-preview svg').isVisible());
 assert.equal(await page.locator('#toggle-animation').getAttribute('aria-pressed'),'true');
 await page.emulateMedia({reducedMotion:'no-preference'});
 await page.locator('[data-feature="1"]').click();
 await page.waitForFunction(()=>document.querySelector('.screen').classList.contains('call-connected'));
 await page.locator('#toggle-animation').click();
 assert(await page.locator('.screen').evaluate(e=>e.classList.contains('animation-paused')));
 for(const primary of await page.locator('.intro .primary,.catalog-actions .primary').all()){
  await primary.hover();await page.waitForTimeout(250);
  assert.equal(await primary.evaluate(e=>getComputedStyle(e).backgroundColor),'rgb(6, 108, 118)','primary links and buttons share hover styling');
 }
 const catalogRequests=[];page.on('request',r=>{if(r.url().endsWith('/v1/boards'))catalogRequests.push(r.url());});
 await page.route(base+'/',async route=>{const response=await route.fetch();const html=await response.text();await route.fulfill({response,body:html.replace('<!-- public:bootstrap -->','<script id="site-brand-data" type="application/json">{"name":"服务端站点","name_en":"SSR site","revision":7}</script><script id="public-catalog-data" type="application/json">{"status":"ready","boards":[{"name":"SSR board","capabilities":[],"chip":"TEST"}]}</script>')});});
 await page.goto(base+'/');await page.locator('.catalog-card').first().waitFor();
 assert.equal(await page.locator('.catalog-card h3').innerText(),'SSR board');
 assert.equal(await page.locator('.brand [data-i18n="site.brand"]').innerText(),'SSR site');
 assert.equal(catalogRequests.length,0);
 await page.unroute(base+'/');
 failed=true;await page.goto(base+'/');await page.locator('#home-board-retry').waitFor();failed=false;await page.locator('#home-board-retry').click();await page.locator('.catalog-card').first().waitFor();
 failed=true;await page.goto(base+'/boards');await page.locator('#catalog-error').waitFor();failed=false;await page.locator('#retry').click();await page.locator('.featured-card').waitFor();assert(await page.locator('#catalog-error').isHidden());
 await page.goto(base+'/boards#sample');await page.getByRole('dialog').waitFor();await page.keyboard.press('Escape');await page.waitForURL(base+'/boards');assert.equal(new URL(page.url()).hash,'');
 for(const url of ['/','/boards']) {
  await page.setViewportSize({width:320,height:900});await page.goto(base+url);
  await page.waitForFunction(()=>window.SiteI18n);
  await page.evaluate(()=>SiteI18n.setBrand({name:'名称'.repeat(20),name_en:'Long brand '.repeat(3)}));
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'long configured brand overflow '+url);
 }
 assert.deepEqual(errors,[]);console.log('PASS public pages: 7 viewport widths, Chinese/English, mobile menu Escape/outside click, terminal keyboard tabs, email link, board capabilities/detail/resources, retry and deep link');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
