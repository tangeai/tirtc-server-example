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
 await page.route('**/v1/boards',route=>route.fulfill({json:failed?{code:50000,msg:'目录加载失败'}:{code:200,data:{boards:[{name:'小钛开发板完整型号与名称检查',model:'ESP32-S3-DISPLAY-LONG-MODEL-NUMBER',vendor:'测试厂商',chip:'ESP32-S3',adaptation_status:'ready',summary:'用于浏览器交互验证的测试开发板。',detail_slug:'sample',capabilities:['AI 对话','实时音视频','设备对讲'],flashing_guide_url:'https://example.test/guide'}]}}}));
 for(const width of [320,390,768,1440,1920]){
  await page.setViewportSize({width,height:900});
  for(const url of ['/','/boards']){
   await page.goto(base+url);await page.waitForFunction(()=>window.SiteI18n);
   if(url==='/boards')await page.locator('.featured-card').waitFor();
   for(const language of ['zh-CN','en']){
    if(await page.locator('html').getAttribute('lang')!==language)await page.locator('[data-site-language-toggle]').click();
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),url+' overflow '+width+' '+language);
    if(width<=768){const menu=page.locator('.menu-button');const menuBox=await menu.boundingBox();assert(menuBox.width>=44&&menuBox.height>=44);await menu.click();await page.keyboard.press('Escape');assert.equal(await menu.getAttribute('aria-expanded'),'false');assert(await menu.evaluate(e=>e===document.activeElement));await menu.click();await page.mouse.click(2,850);assert.equal(await menu.getAttribute('aria-expanded'),'false');}
    if(url==='/'){
     assert.equal(await page.locator('a[href^="mailto:"]').getAttribute('href'),'mailto:'+await page.locator('a[href^="mailto:"]').innerText());
     const tabs=page.locator('[data-terminal-platform]');await tabs.first().focus();await page.keyboard.press('ArrowRight');assert.equal(await tabs.nth(1).getAttribute('aria-selected'),'true');
    }else{
     assert.match(await page.locator('.board-capabilities').innerText(),/实时音视频/);
     await page.locator('.featured-card .detail').click();await page.getByRole('dialog',{name:'小钛开发板完整型号与名称检查'}).waitFor();
     assert(await page.locator('#detail-guide').isVisible());assert(await page.locator('#detail-buy').isHidden());
     const box=await page.locator('#detail-dialog').boundingBox();assert(box.x>=0&&box.x+box.width<=width);
     await page.keyboard.press('Escape');assert(await page.locator('#detail-dialog').isHidden());
    }
    await page.evaluate(()=>scrollTo(0,0));await page.screenshot({path:root+'/../output/playwright/public-review-'+(url==='/'?'home':'boards')+'-'+language+'-'+width+'.png'});
   }
  }
 }
 failed=true;await page.goto(base+'/boards');await page.locator('#catalog-error').waitFor();failed=false;await page.locator('#retry').click();await page.locator('.featured-card').waitFor();assert(await page.locator('#catalog-error').isHidden());
 await page.goto(base+'/boards#sample');await page.getByRole('dialog').waitFor();await page.keyboard.press('Escape');await page.waitForURL(base+'/boards');assert.equal(new URL(page.url()).hash,'');
 assert.deepEqual(errors,[]);console.log('PASS public pages: 5 viewport widths, Chinese/English, mobile menu Escape/outside click, terminal keyboard tabs, email link, board capabilities/detail/resources, retry and deep link');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
