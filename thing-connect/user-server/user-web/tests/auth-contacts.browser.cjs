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
 let captcha=true,room={desired_state:'left',state:'left'},failRead=false,failLogin=true,contactName='朋友',delayRemark;
 await page.route('**/v1/**',async route=>{const req=route.request(),u=new URL(req.url());if(u.pathname.endsWith('/language.js'))return route.continue();requests.push({path:u.pathname,body:req.postDataJSON(),method:req.method()});
 let data={};
 if(u.pathname==='/v1/config/captcha')data={enabled:captcha,provider:'tencent',captcha_id:'test'};
 else if(u.pathname==='/v1/user/login'){if(failLogin)return route.fulfill({json:{code:4091,msg:'邮箱或密码错误'}});data={token:'test-token'};}
 else if(u.pathname.includes('send-code'))data={};
 else if(u.pathname==='/v1/user/device/list')data=[{device_id:'one',device_name:'设备一',status:1},{device_id:'two',device_name:'设备二',status:1}];
 else if(u.pathname==='/v1/user/device/bind')data={device_id:'new-device'};
 else if(u.pathname==='/v1/call/user/contacts')data={contacts:[{id:1,type:'device',source:'manual',device_id:'peer',device_name:contactName}]};
 else if(u.pathname==='/v1/call/user/contacts/pending')data={pending:[]};
 else if(u.pathname==='/v1/call/user/contacts/remark'){if(delayRemark)await delayRemark;data={};}
 else if(u.pathname==='/v1/call/user/contacts/request')data={status:'pending'};
 else if(u.pathname.startsWith('/v1/call/group/web/device/')){if(req.method()==='POST'){room={desired_state:'joined',state:'waiting_device',room_id:1,assignment_version:2,room_code:req.postDataJSON().room_code||'001234',online_count:0};data=room;}else{if(failRead)return route.fulfill({status:503,json:{code:50000,msg:'暂时失败'}});data={...room,online_count:3};}}
 else throw new Error('unexpected '+u.pathname);
 await route.fulfill({json:{code:200,data}});
 });
 let releaseSDK;const sdkReady=new Promise(r=>releaseSDK=r);
 await page.route('https://turing.captcha.qcloud.com/TJCaptcha.js',async r=>{await sdkReady;await r.fulfill({contentType:'text/javascript',body:'window.TencentCaptcha=class {constructor(id,cb){this.cb=cb}show(){window.challengeOpens=(window.challengeOpens||0)+1;window.finishChallenge=()=>this.cb({ret:0,ticket:"ticket",randstr:"rand"})}}'});});
 await page.goto(base+'/register');await page.getByLabel('邮箱',{exact:true}).fill('first@example.test');await page.getByLabel('我已阅读并同意').check();await page.getByRole('button',{name:'获取验证码',exact:true}).click();
 await page.getByRole('button',{name:'取消验证',exact:true}).click();await page.getByLabel('邮箱',{exact:true}).fill('second@example.test');await page.getByRole('button',{name:'获取验证码',exact:true}).click();releaseSDK();await page.waitForFunction(()=>window.challengeOpens===1);await page.evaluate(()=>window.finishChallenge());
 await page.getByRole('button',{name:/s 后重新获取/}).waitFor();assert.equal(requests.find(r=>r.path==='/v1/user/send-code').body.email,'second@example.test');
 await page.getByRole('radio',{name:'登录',exact:true}).locator('..').click();await page.getByLabel('邮箱',{exact:true}).fill('login@example.test');await page.getByLabel('密码',{exact:true}).fill('secret123');
 await page.getByRole('button',{name:'登录',exact:true}).last().click();assert.equal(requests.filter(r=>r.path==='/v1/user/login').length,0);await page.getByLabel('我已阅读并同意').check();await page.getByRole('button',{name:'登录',exact:true}).last().click();await page.waitForFunction(()=>window.challengeOpens===2);await page.evaluate(()=>window.finishChallenge());await page.getByRole('alert').filter({hasText:'邮箱或密码错误'}).waitFor();assert.equal(requests.filter(r=>r.path==='/v1/user/login').length,1);
 for(const width of [320,375,390,768,1440]){await page.setViewportSize({width,height:850});for(const route of ['/login','/register','/forgot-password']){await page.goto(base+route);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),route+' overflow '+width);} }
 await page.evaluate(()=>localStorage.setItem('token','browser-token'));
 await page.goto(base+'/bind');await page.getByLabel('6 位数字验证码').fill('a12b34567');assert.equal(await page.getByLabel('6 位数字验证码').inputValue(),'123456');await page.getByLabel('6 位数字验证码').fill('123456');await page.getByRole('button',{name:'绑定设备',exact:true}).click();await page.getByRole('heading',{name:'绑定成功！'}).waitFor();
 await page.goto(base+'/room?device_id=one');await page.getByRole('button',{name:'创建房间',exact:true}).waitFor();await page.getByLabel('6 位房间号').fill('ab0012cd3456');assert.equal(await page.getByLabel('6 位房间号').inputValue(),'001234');await page.getByRole('button',{name:'加入房间',exact:true}).click();await page.getByText('在线设备：3').waitFor();const before=requests.length;await page.evaluate(()=>{dispatchEvent(new Event('focus'));document.dispatchEvent(new Event('visibilitychange'));});await page.waitForTimeout(1100);assert.equal(requests.length,before,'automatic refresh');
 await page.goto(base+'/contacts?device_id=one');await page.getByRole('button',{name:'改备注'}).waitFor();await page.getByRole('button',{name:'改备注'}).click();await page.getByLabel('联系人备注').fill('新的备注');let releaseRemark;delayRemark=new Promise(r=>releaseRemark=r);await page.getByRole('button',{name:'保存',exact:true}).click();await page.getByLabel('当前管理设备').selectOption('two');await page.getByRole('button',{name:'改备注'}).click();await page.getByLabel('联系人备注').fill('设备二草稿');releaseRemark();await page.waitForTimeout(100);assert.equal(await page.getByLabel('联系人备注').inputValue(),'设备二草稿');
 await page.getByRole('button',{name:'取消',exact:true}).click();await page.getByRole('button',{name:'删除',exact:true}).click();await page.getByRole('dialog').waitFor();assert(await page.getByRole('dialog').getByText(/双方都不再是好友/).isVisible());await page.waitForFunction(() => [...document.querySelectorAll('[role=dialog]')].at(-1)?.contains(document.activeElement));await page.keyboard.press('Escape');await page.getByRole('dialog').waitFor({state:'hidden'});assert.equal(await page.getByRole('dialog').count(),0);
 for(const width of [320,375,390,768,1440]){await page.setViewportSize({width,height:850});await page.getByRole('button',{name:'添加联系人',exact:true}).click();assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'contacts overflow '+width);const box=await page.getByRole('dialog').boundingBox();assert(box.width<=width-20);await page.screenshot({path:root+'/../output/playwright/react-contacts-'+width+'.png',fullPage:true});await page.getByRole('button',{name:'关闭弹窗'}).click();}
 assert.deepEqual(errors,[]);console.log('PASS: auth consent, slow SDK/new email, first-click login, 5 viewport auth pages, binding, numeric room code, no focus polling, contact stale response isolation, dialog Escape/X/mobile. Requests='+requests.length);
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
