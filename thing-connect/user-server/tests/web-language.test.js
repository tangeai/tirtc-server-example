const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const runtime = require('../../tests/helpers/web-i18n.cjs');

test('overseas default overrides browser language, explicit preference wins only when allowed', () => {
  assert.equal(runtime({default:'en',allow_switch:true}).getLanguage(), 'en');
  assert.equal(runtime({default:'en',allow_switch:true}, 'zh-CN').getLanguage(), 'zh-CN');
  const locked=runtime({default:'en',allow_switch:false}, 'zh-CN');
  locked.setLanguage('zh-CN'); assert.equal(locked.getLanguage(),'en');
  assert.equal(runtime({default:'auto',allow_switch:true},'invalid').getLanguage(),'zh-CN');
});

test('switching updates bound output and retires superseded bindings', () => {
  const ui=runtime({default:'en',allow_switch:true});
  const label={isConnected:true,textContent:''};
  ui.bindText(label,()=>ui.t('保存'));
  assert.equal(label.textContent,'Save');
  ui.setLanguage('zh-CN');assert.equal(label.textContent,'保存');
  label.textContent='用户自定义内容';ui.setLanguage('en');
  assert.equal(label.textContent,'用户自定义内容');
});

test('English errors use codes and operation fallback, never Chinese upstream messages', () => {
  const ui=runtime({default:'en',allow_switch:true});
  assert.equal(ui.errorMessage({code:40012,msg:'任意服务端说明'}),'Verification failed. Try again.');
  assert.equal(ui.errorMessage({code:4091,msg:'任意服务端说明'}),'Incorrect email or password');
  assert.equal(ui.errorMessage({code:4002,msg:'任意服务端说明'}),'The verification code is incorrect or has expired');
  assert.equal(ui.errorMessage({code:123456,msg:'不应原样显示'},'保存失败'),'Could not save');
  ui.setLanguage('zh-CN');assert.equal(ui.errorMessage({code:123456,msg:'具体中文说明'}),'具体中文说明');
});

test('each page uses its own service bootstrap and each marked key has a dictionary entry', () => {
  const root=path.join(__dirname,'../..');
  const common=JSON.parse(fs.readFileSync(path.join(root,'internal/webui/common.json'),'utf8'));
  for(const [service,dict,files,prefix] of [
    ['user-server','static/user-i18n.json',['static/auth.html','static/bind.html','static/devices.html','static/contacts.html','static/player.html','static/devices.js','static/recognition.js'],'/v1'],
    ['ai-server','static/ai-i18n.json',['static/agent.html'],'/v1/ai'],
    ['call-server','handler/call-i18n.json',['handler/room_page.html'],'/v1/call'],
  ]) {
    const messages={...common,...JSON.parse(fs.readFileSync(path.join(root,service,dict),'utf8'))};
    for(const file of files){
      const source=fs.readFileSync(path.join(root,service,file),'utf8');
      if(file.endsWith('.html'))assert.ok(source.includes(`src="${prefix}/config/language.js"`));
      for(const [,key] of source.matchAll(/WebI18n\.(?:t|mark)\("([^"\\]*)"/g))assert.ok(messages[key],`${service}/${file}: ${key}`);
    }
  }
});

test('static marked copy has translations and session cleanup retains the language preference', () => {
  const root=path.join(__dirname,'../..');
  const common=JSON.parse(fs.readFileSync(path.join(root,'internal/webui/common.json'),'utf8'));
  const decode=value=>value.replace(/&quot;/g,'"').replace(/&#39;/g,"'").replace(/&lt;/g,'<').replace(/&gt;/g,'>').replace(/&amp;/g,'&');
  for(const [folder,dict,files]of [
    ['user-server','static/user-i18n.json',['static/auth.html','static/devices.html','static/bind.html','static/contacts.html','static/player.html']],
    ['ai-server','static/ai-i18n.json',['static/agent.html']],
    ['call-server','handler/call-i18n.json',['handler/room_page.html']],
  ]) {
    const messages={...common,...JSON.parse(fs.readFileSync(path.join(root,folder,dict),'utf8'))};
    for(const file of files){
      const source=fs.readFileSync(path.join(root,folder,file),'utf8');
      for(const [,key]of source.matchAll(/data-ui-i18n="([^"\\]*)"/g))assert.ok(messages[decode(key)],`${folder}/${file}: ${key}`);
      for(const [,attrs]of source.matchAll(/data-ui-attrs="([^"\\]*)"/g))for(const key of Object.values(JSON.parse(decode(attrs))))assert.ok(messages[key],`${folder}/${file}: ${key}`);
    }
  }
  const storage=new Map([['xiaotai.site.language','en'],['token','expired-token']]);
  const ui=runtime({default:'zh-CN',allow_switch:true},null,storage);
  ui.clearLocalSession();
  assert.deepEqual([...storage],[['xiaotai.site.language','en']]);
});
