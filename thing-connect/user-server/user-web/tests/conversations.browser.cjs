const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
const fs=require('fs'),path=require('path'),http=require('http'),assert=require('assert/strict');
const root=path.resolve(__dirname,'../../..');fs.mkdirSync(path.join(root,'../output/playwright'),{recursive:true});
const server=http.createServer((req,res)=>{
 const url=new URL(req.url,'http://localhost');
 if(url.pathname==='/v1/config/language.js'){res.setHeader('Content-Type','text/javascript');res.end('window.WebLanguageConfig={default:"zh-CN",allow_switch:true};'+fs.readFileSync(root+'/internal/webui/runtime.js','utf8')+';WebI18n.addMessages('+fs.readFileSync(root+'/internal/webui/common.json','utf8')+');');return;}
 const file=url.pathname.startsWith('/static/')?root+'/user-server'+url.pathname:root+'/user-server/static/app/index.html';if(!fs.existsSync(file)){res.writeHead(404);res.end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));
});
(async()=>{await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const base='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM||undefined,args:['--no-sandbox']});
 try{
  const context=await browser.newContext({viewport:{width:1366,height:768}}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  let records=Array.from({length:11},(_,i)=>({id:'c'+i,device_id:'one',assistant_id:'role',started_at:`2026-11-${String(11-i).padStart(2,'0')}T09:00:00Z`,reference:'ref-c'+i}));
  let listCalls=0,detailCalls=0,deletes=0,failList=false,failDetail=false,failDelete=false,delayFirst=false,release;
  await page.route('**/v1/**',async route=>{
   const req=route.request(),u=new URL(req.url());if(u.pathname.endsWith('/language.js'))return route.continue();let data={};
   if(u.pathname==='/v1/user/device/list')data=[{device_id:'one',device_name:'客厅小钛',online:true}];
   else if(u.pathname==='/v1/user/me')data={email:'test@example.test'};
   else if(u.pathname==='/v1/ai/roles')data={items:[{id:'role',name:'我的角色',agent_config:{prompt:'原始设定'}}]};
   else if(u.pathname==='/v1/ai/roles/role')data={id:'role',name:'我的角色',agent_config:{prompt:'原始设定'}};
   else if(u.pathname==='/v1/ai/device/one/role')data={role_id:'role',default_role_id:''};
   else if(u.pathname==='/v1/ai/device/one/conversations'){
    listCalls++;if(failList)return route.fulfill({status:502,json:{code:50200,msg:'会话服务暂不可用'}});
    const number=Number(u.searchParams.get('page'));const items=records.slice((number-1)*10,number*10);data={items,page:number,page_size:10,has_more:items.length===10};
   }else if(u.pathname.startsWith('/v1/ai/device/one/conversations/')){
    const id=u.pathname.split('/').pop();assert.equal(req.headers()['x-conversation-reference'],'ref-'+id);
    if(req.method()==='DELETE'){deletes++;if(failDelete){failDelete=false;return route.fulfill({status:502,json:{code:50200,msg:'删除失败，请重试'}});}records=records.filter(r=>r.id!==id);}
    else {detailCalls++;if(delayFirst&&id==='c0'){delayFirst=false;await new Promise(resolve=>release=resolve);}
     if(failDetail){failDetail=false;return route.fulfill({status:502,json:{code:50200,msg:'读取失败，请重试'}});}
     data={items:[{role:'user',content:'问题 '+id+' <img src=x onerror=alert(1)>',time:'2026-11-07T09:00:00Z'},{role:'assistant',content:'回答 '+id+'\n'+('这是一段清晰的回答。'.repeat(12)),time:'2026-11-07T09:00:01Z'}]};
    }
   }else if(u.pathname.includes('/v1/ai/'))data={items:[]};
   else if(u.pathname.includes('/v1/recognition/'))data={policy:{enabled:false},schemes:[]};
   else if(u.pathname.includes('/v1/call/'))data={desired_state:'left',state:'left',online:true};
   await route.fulfill({json:{code:200,data}}).catch(()=>{});
  });
  await page.goto(base+'/login');await page.evaluate(()=>localStorage.setItem('token','test'));await page.goto(base+'/agent?device_id=one');
  await page.getByLabel('智能体名称',{exact:true}).fill('未保存名称');
  await page.getByRole('button',{name:'会话记录',exact:true}).click();
  const dialog=page.getByRole('dialog',{name:'客厅小钛 · 会话记录',exact:true});
  await dialog.getByText(/回答 c0/).waitFor();assert.equal(await dialog.locator('.history-bubble img').count(),0);
  const calls=listCalls;await page.evaluate(()=>{window.dispatchEvent(new Event('focus'));document.dispatchEvent(new Event('visibilitychange'));});await page.waitForTimeout(250);assert.equal(listCalls,calls);
  await dialog.getByRole('button',{name:'下一页',exact:true}).click();await dialog.getByText(/回答 c10/).waitFor();
  await dialog.getByRole('button',{name:'删除会话',exact:true}).click();await page.getByRole('dialog',{name:'删除会话',exact:true}).getByRole('button',{name:'取消',exact:true}).click();assert.equal(deletes,0);
  failDelete=true;await dialog.getByRole('button',{name:'删除会话',exact:true}).click();await page.getByRole('button',{name:'确认删除',exact:true}).click();await dialog.getByRole('alert').waitFor();assert.equal(records.length,11);
  await dialog.getByRole('button',{name:'删除会话',exact:true}).click();await page.getByRole('button',{name:'确认删除',exact:true}).click();await dialog.getByText(/回答 c0/).waitFor();assert.equal(records.length,10);await dialog.getByText('第 1 页',{exact:true}).waitFor();
  // A delayed response for a previously selected conversation cannot replace the current one.
  delayFirst=true;await dialog.locator('.history-entry').first().click();while(!release)await page.waitForTimeout(10);
  await dialog.locator('.history-entry').nth(1).click();await dialog.getByText(/回答 c1/).waitFor();release();await page.waitForTimeout(100);assert.equal(await dialog.getByText(/回答 c0/).count(),0);
  failDetail=true;await dialog.locator('.history-entry').nth(2).click();await dialog.getByRole('alert').waitFor();await dialog.getByRole('button',{name:'重试',exact:true}).click();await dialog.getByText(/回答 c2/).waitFor();
  for(const viewport of [{width:1366,height:768},{width:390,height:844},{width:320,height:700}]){
   await page.setViewportSize(viewport);
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   const bounds=await dialog.boundingBox();assert(bounds.x>=0&&bounds.y>=0&&bounds.y+bounds.height<=viewport.height+1);
   if(viewport.width<641){await dialog.getByRole('button',{name:'返回列表',exact:false}).click();await dialog.locator('.history-entry').nth(1).click();await dialog.getByText(/回答 c1/).waitFor();}
   await page.screenshot({path:root+'/../output/playwright/conversations-'+viewport.width+'.png',fullPage:true});
  }
  await dialog.getByRole('button',{name:'关闭弹窗',exact:true}).click();assert.equal(await page.getByLabel('智能体名称',{exact:true}).inputValue(),'未保存名称');
  // Empty and failed lists remain usable and can be refreshed.
  records=[];await page.getByRole('button',{name:'会话记录',exact:true}).click();await dialog.getByText('暂无会话记录',{exact:true}).waitFor();
  failList=true;await dialog.getByRole('button',{name:'刷新',exact:true}).click();await dialog.getByRole('alert').waitFor();failList=false;await dialog.getByRole('button',{name:'刷新',exact:true}).click();await dialog.getByText('暂无会话记录',{exact:true}).waitFor();
  await dialog.getByRole('button',{name:'关闭弹窗',exact:true}).click();
  await page.getByRole('button',{name:'放弃修改',exact:true}).click();await page.getByRole('dialog').getByRole('button',{name:'放弃修改',exact:true}).click();
  await page.goto(base+'/devices');await page.getByText('更多 ▾',{exact:true}).click();await page.getByRole('button',{name:'会话记录',exact:true}).click();await dialog.getByText('暂无会话记录',{exact:true}).waitFor();
  assert.deepEqual(errors,[]);console.log('PASS history entry points, draft preservation, pagination, plain text, deletion cancel/failure/success, stale details, empty/error/retry, no automatic polling, desktop/mobile');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1});
