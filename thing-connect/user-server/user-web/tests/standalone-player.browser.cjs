const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const assert = require('node:assert/strict');
const path = require('node:path');
const root = path.resolve(__dirname, '../../..');
const app = 'https://player.test';
const sdk = `
  export const TiRtc={initialize(){},async videoOutputReady(){}};
  export const TiRtcInitOptions=x=>x;
  export class TiRtcConn{async connect(){} disconnect(){} subscribeVideo(){} unsubscribeVideo(){}}
  export const TiRtcVideoOutput=()=>({attach(){},detach(){}});
  export const TiRtcAudioOutput=TiRtcVideoOutput;
  export class TiRtcAudioInput{setOptions(){} start(){window.mic=true} stop(){window.mic=false} attach(){} detach(){}}
`;
async function check(browser, embedded, expiry) {
  const page = await browser.newPage({viewport:{width:embedded?390:1440,height:840}});
  try {
    const requests = [], errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.addInitScript(() => localStorage.setItem('token','{"data":"browser"}'));
    let ready;
    const bridgeReady = new Promise(resolve => {ready=resolve});
    await page.route('https://res.wx.qq.com/**', async route => {
      if (expiry === 'early') await bridgeReady;
      await route.fulfill({contentType:'text/javascript',body:`window.wx={miniProgram:{
        reLaunch({url}){(window.logins??=[]).push(url)},navigateBack(){window.wentBack=true}
      }}`});
    });
    await page.route(app+'/**', async route => {
      const url = new URL(route.request().url());
      assert.equal(url.searchParams.has('mini_token'),false);
      if(url.pathname==='/v1/config/language.js')return route.fulfill({contentType:'text/javascript',body:'window.WebLanguageConfig={default:"zh-CN",allow_switch:true};'+require('node:fs').readFileSync(path.join(root,'internal/webui/runtime.js'),'utf8')});
      if(url.pathname.startsWith('/v1/user/')){
        requests.push(route.request().headers().authorization);
        if(expiry==='late') await page.waitForFunction(()=>!!window.wx?.miniProgram);
        if(expiry)return route.fulfill({status:401,body:'unauthorized'});
        const data=url.pathname.endsWith('/list')?[{device_id:'one',device_name:'客厅'}]:{token:'rtc',app_id:'app'};
        return route.fulfill({json:{code:200,data}});
      }
      if(url.pathname==='/static/js/tirtc.es.min.js')return route.fulfill({contentType:'text/javascript',body:sdk});
      const file=url.pathname==='/player'?'static/player/index.html':url.pathname.slice(1);
      if(!file.startsWith('static/'))return route.abort();
      return route.fulfill({path:path.join(root,'user-server',file)});
    });
    await page.goto(app+'/player?device_id=one'+(embedded?'&source=miniprogram#mini_token=native':''),{waitUntil:'domcontentloaded'});
    if(expiry){
      await page.locator('#mini-session-expired').waitFor();
      ready();
      await page.waitForFunction(()=>window.logins?.length===1);
      assert.deepEqual(await page.evaluate(()=>window.logins),['/pages/login/index?expired=1']);
    } else {
      const talk=page.getByRole('button',{name:'按住说话'});
      await talk.focus();
      await page.waitForFunction(()=>document.querySelector('.player-controls button')?.disabled===false);
      await talk.press('Space',{delay:100});
      assert.equal(await page.evaluate(()=>window.mic),false);
      if(embedded){
        await page.getByRole('button',{name:/返回设备列表/}).click();
        assert.equal(await page.evaluate(()=>window.wentBack),true);
      }
    }
    assert.equal(new URL(page.url()).hash,'');
    assert.equal(await page.evaluate(()=>localStorage.getItem('token')),embedded?'{"data":"browser"}':'browser');
    assert.ok(requests.length && requests.every(v=>v==='Bearer '+(embedded?'native':'browser')));
    assert.deepEqual(errors,[]);
  } finally {await page.close();}
}
(async()=>{
 const browser=await chromium.launch({executablePath:process.env.PLAYWRIGHT_CHROMIUM || undefined,args:['--no-sandbox']});
 try {await check(browser,false,false);await check(browser,true,false);await check(browser,true,'early');await check(browser,true,'late');}
 finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
