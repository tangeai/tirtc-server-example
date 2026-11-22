// All registered management pages: desktop/mobile, failure recovery and menu access.
const {chromium}=require('../../../user-server/user-web/node_modules/playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const installFixture=require('./page-fixture.cjs');
const dist=path.resolve(__dirname,'../dist');
const output=path.resolve(__dirname,'../../../../output/playwright/admin-pages');
fs.mkdirSync(output,{recursive:true});
const server=http.createServer((req,res)=>{
 const url=new URL(req.url,'http://localhost');
 const file=url.pathname.startsWith('/admin/assets/')?path.join(dist,url.pathname.slice(7)):path.join(dist,'index.html');
 if(!fs.existsSync(file)){res.writeHead(404).end();return;}
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,args:['--no-sandbox']});
 const results=[];
 try {
  const page=await browser.newPage();const errors=[],bad=[];
  page.on('pageerror',e=>errors.push(e.message));
  page.on('response',r=>{if(r.url().includes('/v1/')&&r.status()>=400)bad.push(new URL(r.url()).pathname+':'+r.status());});
  const routes=await installFixture(page);
  for(const width of [1440,390])for(const [route,menuTitle] of routes){
   const title=route==='/configs/recognition-server'?'AI识别':menuTitle;
   await page.setViewportSize({width,height:900});
   await page.goto(base+'/admin'+route);
   await page.locator('.ant-page-header-heading-title').filter({hasText:title}).waitFor({timeout:15000}).catch(async e=>{console.error({route,errors,bad,body:(await page.locator('body').innerText()).slice(0,1800)});throw e;});
   await page.waitForLoadState('networkidle');
   assert.equal(await page.getByText('页面未注册',{exact:true}).count(),0,route);
   assert.equal(await page.getByText('无权访问',{exact:true}).count(),0,route);
   assert.deepEqual(errors,[],route+' browser errors');assert.deepEqual(bad,[],route+' failed API requests');
   await page.screenshot({path:path.join(output,route.replaceAll('/','_')+'-'+width+'.png'),fullPage:true});
   results.push({route,width,result:'pass'});console.log('PASS',route,width);
  }
  // Same query must refetch; a failed list must be recoverable without navigation.
  await page.setViewportSize({width:1440,height:900});await page.goto(base+'/admin/users');
  await page.locator('.ant-page-header-heading-title').filter({hasText:'用户管理'}).waitFor();await page.waitForLoadState('networkidle');
  let fail=true,queries=0;
  await page.route('**/v1/admin/users?**',async route=>{queries++;if(fail)await route.fulfill({status:500,json:{code:50000,msg:'测试查询失败'}});else await route.fallback();});
  await page.getByRole('button',{name:/查\s*询/}).click();await page.getByText('测试查询失败',{exact:true}).first().waitFor();
  fail=false;await page.getByRole('button',{name:/查\s*询/}).click();await page.waitForLoadState('networkidle');
  assert.equal(queries,2,'same query must retry');
  await page.getByText('user@example.test',{exact:true}).first().waitFor();
  console.log('PASS repeated query and failed-query recovery');results.push({scenario:'query-recovery',result:'pass'});
  const restricted=await browser.newPage();await installFixture(restricted,['/overview']);
  let businessRequests=0;restricted.on('request',r=>{if(new URL(r.url()).pathname==='/v1/admin/users')businessRequests++;});
  await restricted.goto(base+'/admin/users');await restricted.getByText('无权访问',{exact:true}).waitFor();
  assert.equal(businessRequests,0,'forbidden page must not fetch user data');
  results.push({scenario:'menu-access',result:'pass'});console.log('PASS menu access denial');
 } finally {fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({at:new Date().toISOString(),results},null,2));await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
