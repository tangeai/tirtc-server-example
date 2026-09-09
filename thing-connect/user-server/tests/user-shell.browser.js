async page => {
 const base='http://dev-demo-open.tangeai.cn:8080';
 await page.unrouteAll();
 await page.addInitScript(()=>{if(!sessionStorage.getItem('testing-expiry'))localStorage.setItem('token','browser-test-token');});
 let links=[],expired=false,offline=true;
 const check=(v,m)=>{if(!v)throw Error(m)};
 await page.route('**/v1/config/navigation',r=>r.fulfill({json:{code:200,data:{links}}}));
 await page.route('**/v1/user/me',r=>r.fulfill({status:expired?401:200,json:expired?{code:401}:{code:200,data:{user_id:1,email:'test@example.com'}}}));
 await page.route('**/v1/user/device/list',r=>r.fulfill({json:{code:200,data:[{device_id:'test-device',device_name:'测试设备',online:!offline}]}}));
 await page.route('**/v1/ai/device/**/role',r=>r.fulfill({json:{code:200,data:{role_id:''}}}));
 await page.route('**/v1/call/room/web/device/**',r=>r.fulfill({json:{code:200,data:{desired_state:'joined',state:offline?'waiting_device':'joined',room_code:'610337',online:!offline,online_count:2}}}));
 await page.goto(base+'/devices');
 await page.locator('[data-room]').filter({hasText:/^610337$/}).waitFor();
 check(await page.locator('[data-navigation]').isHidden(),'empty navigation visible');
 await page.locator('#user-menu-trigger').click();await page.getByText('test@example.com',{exact:true}).waitFor();
 check(await page.getByRole('link',{name:'修改密码'}).getAttribute('href')==='/forgot-password','password entry wrong');
 await page.keyboard.press('Escape');check(await page.locator('#user-menu').isHidden(),'account menu Escape failed');
 links=Array.from({length:3},(_,i)=>({name:'文档链接'+i,url:'https://example.com/'+i,enabled:true}));
 for(const width of [320,390,1440]){
  await page.setViewportSize({width,height:900});await page.reload();await page.locator('[data-navigation]').waitFor();
  if(width<768)await page.locator('[data-navigation] summary').click();
  check(await page.locator('[data-navigation] a:visible').count()===3,'missing links');
  check(await page.locator('[data-navigation] a').first().getAttribute('target')==='_blank','link target');
  check(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'horizontal overflow '+width);
  await page.screenshot({path:'output/playwright/navigation-'+width+'.png',fullPage:true});
 }
 await page.locator('.device-summary button').last().click();await page.waitForURL('**/v1/call/room/page?device_id=test-device');
 await page.goto(base+'/devices');await page.locator('.device-summary button').first().click();await page.waitForURL('**/v1/ai/agent?device_id=test-device');
 await page.goto(base+'/devices');await page.evaluate(()=>sessionStorage.setItem('testing-expiry','1'));expired=true;await page.reload();await page.waitForURL('**/login');
 await page.getByText('登录状态已失效，请重新登录',{exact:true}).waitFor();
 check(await page.evaluate(()=>localStorage.getItem('token'))===null,'stale token retained');
 await page.evaluate(()=>sessionStorage.removeItem('testing-expiry'));
 return 'PASS: navigation 0/3 links, mobile/desktop, account menu, summary routes, expired login notice';
}
