// Run after admin-web build; uses synthetic data and no production accounts.
const {chromium}=require('../../../user-server/user-web/node_modules/playwright');
const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),http=require('node:http');
const dist=path.resolve(__dirname,'../dist');
const server=http.createServer((req,res)=>{const url=new URL(req.url,'http://localhost');const file=url.pathname.startsWith('/admin/assets/')?path.join(dist,url.pathname.slice('/admin/'.length)):path.join(dist,'index.html');if(!fs.existsSync(file)){res.writeHead(404).end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(fs.readFileSync(file));});
(async()=>{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const browser=await chromium.launch({headless:true,executablePath:process.env.PLAYWRIGHT_CHROMIUM,args:['--no-sandbox']});
 try {
  const page=await browser.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  let empty=false;const device={id:1,device_id:'test-device',device_name:'测试设备',user_id:1,online:false,presence_known:true,hardware:{chip_model:'ESP32-S3'},firmware_version:'1.0',profiles:{stream:{up_video_mt:['h264','mjpeg'],up_audio_streamid:0},call:{no_video:true}}};
  await page.route('**/v1/**',async route=>{const p=new URL(route.request().url()).pathname;let data={};
   if(p==='/v1/setup/status')return route.fulfill({status:404});
   if(p.endsWith('/auth/refresh'))data={access_token:'synthetic-token'};
   else if(p.endsWith('/me/navigation'))data={menus:[{id:1,parent_id:0,menu_code:'devices',name:'设备管理',path:'/devices',icon:'',menu_type:2,sort_no:0}]};
   else if(p.endsWith('/me'))data={user:{id:1,email:'test@example.test',must_change_password:0}};
   else if(p.endsWith('/devices/test-device'))data=empty?{...device,profiles:undefined}:device;
   else if(p.endsWith('/devices'))data={items:[device],total:1,page:1,page_size:20};
   else data={items:[],total:0};
   await route.fulfill({json:{code:200,msg:'ok',data}});
  });
  const open=async()=>{await page.goto('http://127.0.0.1:'+server.address().port+'/admin/devices');await page.getByRole('button',{name:'详情与上报属性'}).click();await page.getByText('设备上报属性',{exact:true}).waitFor();};
  await open();const drawer=page.getByRole('dialog');
  await drawer.getByRole('tab',{name:'实时音视频',exact:true}).click();await drawer.getByText('h264 / mjpeg',{exact:true}).waitFor();
  await drawer.getByText('查看能力数据 JSON',{exact:true}).click();assert.match(await drawer.locator('.profile-json').innerText(),/"up_audio_streamid": 0/);
  await drawer.getByRole('tab',{name:'设备通话',exact:true}).click();await drawer.getByText('是',{exact:true}).waitFor();
  await drawer.getByRole('tab',{name:'微信通话',exact:true}).click();await drawer.getByText('此场景尚未上报',{exact:true}).waitFor();
  await page.setViewportSize({width:390,height:844});await page.waitForFunction(()=>{const box=document.querySelector('[role="dialog"]').getBoundingClientRect();return box.x>=-1&&box.right<=innerWidth+1;});
  empty=true;await open();await drawer.getByText('暂无可展示的能力上报',{exact:true}).waitFor();
  assert.deepEqual(errors,[]);console.log('PASS admin device profile: offline device, three scenes, codec arrays, boolean/zero values, JSON, absent profiles and mobile drawer');
 } finally {await browser.close();server.close();}
})().catch(e=>{console.error(e);server.close();process.exitCode=1;});
