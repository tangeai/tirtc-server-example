const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const html = fs.readFileSync(require('node:path').join(__dirname, '../static/auth.html'), 'utf8');
function source(name, next) { return html.slice(html.indexOf('async function '+name+'('), html.indexOf('async function '+next+'(')); }
for (const [name,next] of [['doLogin','_submitLoginWithToken'],['doSendCode','_submitSendCode'],['doSendResetCode','_submitResetCode']]) {
  test(name + ': first click opens challenge without refreshing it away', async () => {
    let ready = true, opened = 0;
    const instance = {refresh() { ready = false; }, verify() { if(ready) opened++; }};
    const element = {value:'test@example.com',disabled:false,dataset:{},checkValidity:()=>true};
    const context = {requireLegalConsent:()=>true,CAPTCHA_ID:'test',captchaInitPromise:Promise.resolve(true),loginCaptchaToken:null,regCaptchaToken:null,
      loginCaptchaIns:instance,regCaptchaIns:instance,emailRevisions:{reg:0,reset:0},_regSendAfterVerify:null,
      $:()=>element,showErr(){},setBtn(){},setupLoginCaptcha(){},setupRegCaptcha(){},waitForCaptcha:async get=>get(),
      _submitSendCode(){},_submitResetCode(){},_submitLoginWithToken(){}};
    vm.createContext(context);vm.runInContext(html.slice(html.indexOf('const pendingCaptchaOpen'),html.indexOf('async function waitForCaptcha')) + source(name,next),context);
    await context[name]({preventDefault(){}});
    assert.equal(opened,1,'one click must open the challenge even when refresh is asynchronous');
  });
}

test('slow SDK retains one open intent and coalesces repeated clicks', async () => {
  let release, opens = 0;
  const ready = new Promise(resolve => { release = resolve; });
  const context = {waitForCaptcha: () => ready};
  vm.createContext(context);
  vm.runInContext(html.slice(html.indexOf('const pendingCaptchaOpen'),html.indexOf('async function waitForCaptcha')),context);
  const setup = () => {};
  const first = context.openCaptcha(()=>null,setup);
  const second = context.openCaptcha(()=>null,setup);
  assert.equal(opens,0);
  release({verify(){ opens++; }});
  await Promise.all([first,second]);
  assert.equal(opens,1);
});

test('email changed while SDK loads cancels stale popup', async () => {
  let release, current = true, opens = 0;
  const context = {waitForCaptcha: () => new Promise(resolve=>{release=resolve;})};
  vm.createContext(context);
  vm.runInContext(html.slice(html.indexOf('const pendingCaptchaOpen'),html.indexOf('async function waitForCaptcha')),context);
  const pending = context.openCaptcha(()=>null,()=>{},()=>current);
  current=false;release({verify(){opens++;}});await pending;
  assert.equal(opens,0);
});
