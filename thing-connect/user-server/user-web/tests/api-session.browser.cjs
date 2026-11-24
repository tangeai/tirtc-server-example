const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict'),ts=require('typescript');
const code=ts.transpileModule(fs.readFileSync(path.join(__dirname,'../src/lib/api.ts'),'utf8'),{compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}}).outputText;
const server=http.createServer((req,res)=>{
 if(req.url==='/api.js'){res.setHeader('Content-Type','text/javascript');res.end(code);return;}
 res.setHeader('Content-Type','text/html');res.end('<script type="module">import {request} from "/api.js";window.testRequest=request;</script>');
});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM||undefined,args:['--no-sandbox']});
 try{
 const context=await browser.newContext();const a=await context.newPage(),b=await context.newPage();
 await Promise.all([a.goto(base),b.goto(base)]);await a.waitForFunction(()=>!!window.testRequest);
 for(const status of [200,401]){
 await b.evaluate(()=>localStorage.setItem('token','old-account'));
 let intercepted;const ready=new Promise(resolve=>{intercepted=resolve});let pendingRoute;
 await a.route('**/v1/user/device/list',route=>{pendingRoute=route;intercepted();});
 await a.evaluate(()=>{window.observation=window.testRequest('/v1/user/device/list').then(data=>({data}),error=>({code:error.code}));});
 await ready;assert.equal(pendingRoute.request().headers().authorization,'Bearer old-account');
 await b.evaluate(()=>localStorage.setItem('token','new-account'));
 await pendingRoute.fulfill({status,contentType:'application/json',body:JSON.stringify({code:status,data:['old-device']})});
 const result=await a.evaluate(()=>window.observation);assert.equal(result.code,-2);assert.equal(result.data,undefined);
 assert.equal(await b.evaluate(()=>localStorage.getItem('token')),'new-account');assert.equal(a.url(),base+'/');
 await a.unroute('**/v1/user/device/list');
 }
 console.log('PASS: real Chromium shared-storage tabs reject stale success/401 responses without deleting the new login.');
 }finally{await browser.close();server.close();}
})().catch(error=>{console.error(error);server.close();process.exitCode=1});
