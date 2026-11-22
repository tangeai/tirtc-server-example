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
 try {
  for(const file of ['interaction-cases.cjs','form-cases.cjs']){
   const page=await browser.newPage();await installFixture(page);
   const result=await require('./'+file)(page,base);
   if(result.error){console.error(result);throw new Error(result.error);}
   const passed=Array.isArray(result)?result:result.passed;
   assert(passed.length>0,'no interactions exercised');
   console.log('PASS',file,JSON.stringify(passed));await page.close();
  }
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
