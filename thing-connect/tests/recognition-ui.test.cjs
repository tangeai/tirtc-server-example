const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

// Run the real dialog handlers with only DOM construction and HTTP replaced.
class Element {
  constructor(tag) { this.tagName=tag; this.children=[]; this.dataset={}; }
  append(...nodes) { for (const node of nodes) { node.parent=this; this.children.push(node); } }
  replaceChildren(...nodes) { this.children=[]; this.append(...nodes); }
  replaceWith(node) { const i=this.parent.children.indexOf(this); node.parent=this.parent; this.parent.children[i]=node; }
  setAttribute() {}
  showModal() { this.open=true; }
  close() { this.open=false; }
}
const all = node => [node,...node.children.flatMap(all)];
async function setup(count=1, respond=()=>undefined) {
  const body=new Element('body'), writes=[], messages=[];
  const cards=['device-1','device-2','device-3'].map(device=>({dataset:{cardDevice:device},label:{textContent:'已关闭'},querySelector(){return this.label;}}));
  const types=Array.from({length:count},(_,i)=>({scheme_id:`type-${i}`,scheme_version:1,name:`声音${i}`,description:`说明${i}<b>原样文本</b>`,output_mode:'text_audio',limits:{max_audio_seconds:10}}));
  const context={WebI18n:require("./helpers/web-i18n.cjs")(),window:{},document:{body,createElement:tag=>new Element(tag),querySelectorAll:()=>cards},toast:msg=>messages.push(msg),api:async(url,options)=>{
    if(options?.method==='PUT') writes.push(JSON.parse(options.body));
    const response=respond(url,options);
    if(response!==undefined)return await response;
    if(options?.method==='PUT') { const submitted=JSON.parse(options.body);return {code:200,data:{policy:{enabled:submitted.enabled,revision:submitted.expected_revision+1}}}; }
    return {code:200,data:url.endsWith('/types')?types:{schemes:[],policy:{enabled:true,revision:4,monthly_limit:100},usage:{used:0}}};
  }};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../user-server/static/recognition.js'),'utf8'),context);
  await context.window.openRecognition('device-1');
  const click=async text=>{const b=all(body).find(n=>n.tagName==='button'&&n.textContent===text); assert.ok(b,`button ${text}`); await b.onclick();};
  const select=(index=0,checked=true)=>{const picker=all(body).find(n=>n.className==='recognition-picker');const inputs=all(picker).filter(n=>n.tagName==='input'); inputs[index].checked=checked; inputs[index].onchange();};
  return {body,writes,messages,click,select,cards,open:context.window.openRecognition};
}
test('saving includes checked types without a separate add confirmation',async()=>{
  const ui=await setup(); await ui.click('＋ 添加音频类型'); ui.select(); await ui.click('保存设置');
  assert.equal(ui.writes.length,1,ui.messages.join(','));
  assert.deepEqual(ui.writes[0].schemes,[{scheme_id:'type-0',scheme_version:1}]);
  assert.equal(ui.writes[0].expected_revision,4);
});
test('unchecking or cancelling picker does not save a pending selection',async()=>{
  for(const cancel of [false,true]) {
    const ui=await setup(); await ui.click('＋ 添加音频类型'); ui.select();
    if(cancel) await ui.click('取消'); else ui.select(0,false);
    await ui.click('保存设置'); assert.equal(ui.writes.length,0);
  }
});
test('explicit add then save does not duplicate a type',async()=>{
  const ui=await setup(); await ui.click('＋ 添加音频类型'); ui.select(); await ui.click('添加所选'); await ui.click('保存设置');
  assert.equal(ui.writes.length,1); assert.equal(ui.writes[0].schemes.length,1);
});
test('saving pending selection enforces eight type limit',async()=>{
  const ui=await setup(9); await ui.click('＋ 添加音频类型'); for(let i=0;i<9;i++)ui.select(i);
  await ui.click('保存设置'); assert.equal(ui.writes.length,0); assert.ok(ui.messages.includes('最多选择8个识别类型'));
});

const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};};
const deviceState=()=>({code:200,data:{schemes:[],policy:{enabled:true,revision:4,monthly_limit:100},usage:{used:0}}});
const dialog=ui=>all(ui.body).find(n=>n.tagName==='dialog');
test('late save updates only its device and leaves a newer dialog usable',async()=>{
  const pending=deferred();
  const ui=await setup(1,(url,options)=>options?.method==='PUT'&&url.includes('/device-1/')?pending.promise:undefined);
  await ui.click('＋ 添加音频类型');ui.select();
  const save=ui.click('保存设置');
  await ui.click('关闭');await ui.open('device-2');
  assert.equal(dialog(ui).open,true);
  pending.resolve({code:200,data:{policy:{enabled:true,revision:5}}});await save;
  assert.equal(ui.cards[0].label.textContent,'已开启 · 1项');
  assert.equal(ui.cards[1].label.textContent,'已关闭');
  assert.equal(dialog(ui).open,true);
  await ui.click('＋ 添加音频类型');ui.select();await ui.click('保存设置');
  assert.equal(ui.writes.length,2);
  assert.equal(ui.cards[1].label.textContent,'已开启 · 1项');
});
test('late save failure does not show an error in a newer dialog',async()=>{
  const pending=deferred();
  const ui=await setup(1,(_url,options)=>options?.method==='PUT'?pending.promise:undefined);
  await ui.click('＋ 添加音频类型');ui.select();const save=ui.click('保存设置');
  await ui.click('关闭');await ui.open('device-2');
  pending.reject(new Error('old save failed'));await save;
  assert.equal(dialog(ui).open,true);assert.deepEqual(ui.messages,[]);
});
test('late configuration loads and failures cannot replace a newer device dialog',async()=>{
  for(const failed of [false,true]){
    const pending=deferred();
    const ui=await setup(1,url=>url.includes('/device-2/')?pending.promise:undefined);
    const first=ui.open('device-2');await ui.open('device-3');
    if(failed)pending.reject(new Error('old load failed'));else pending.resolve(deviceState());
    await first;
    assert.equal(dialog(ui).open,true);
    assert.ok(all(ui.body).some(n=>n.tagName==='p'&&n.textContent==='device-3'));
    assert.deepEqual(ui.messages,[]);
  }
});
test('escape dismisses the dialog without a late save success toast',async()=>{
  const pending=deferred();
  const ui=await setup(1,(_url,options)=>options?.method==='PUT'?pending.promise:undefined);
  await ui.click('＋ 添加音频类型');ui.select();const save=ui.click('保存设置');
  dialog(ui).oncancel();dialog(ui).close();
  pending.resolve({code:200,data:{policy:{enabled:true,revision:5}}});await save;
  assert.equal(dialog(ui).open,false);assert.deepEqual(ui.messages,[]);
  assert.equal(ui.cards[0].label.textContent,'已开启 · 1项');
});

test('an older save response cannot overwrite a newer save on the same device',async()=>{
  const oldResponse=deferred();let submitted=0;
  const ui=await setup(1,(url,options)=>{
    if(!url.includes('/device-1/'))return;
    if(options?.method==='PUT'){
      submitted++;
      if(submitted===1)return oldResponse.promise;
      return {code:200,data:{policy:{enabled:false,revision:6}}};
    }
    if(submitted){const state=deviceState();state.data.policy.revision=5;return state;}
  });
  await ui.click('＋ 添加音频类型');ui.select();const firstSave=ui.click('保存设置');
  await ui.click('关闭');await ui.open('device-1');
  const toggle=all(ui.body).find(n=>n.tagName==='input');toggle.checked=false;toggle.onchange();
  await ui.click('保存设置');
  assert.equal(ui.writes[1].expected_revision,5);
  assert.equal(ui.cards[0].label.textContent,'已关闭');
  oldResponse.resolve({code:200,data:{policy:{enabled:true,revision:5}}});await firstSave;
  assert.equal(ui.cards[0].label.textContent,'已关闭');
});

test('reopening a rebound device accepts its reset configuration revision',async()=>{
  let rebound=false;
  const ui=await setup(1,(url,options)=>{
    if(rebound&&url.includes('/device-1/')&&!options){const state=deviceState();state.data.policy.revision=0;return state;}
  });
  await ui.click('＋ 添加音频类型');ui.select();await ui.click('保存设置');
  rebound=true;await ui.open('device-1');
  assert.equal(dialog(ui).open,true);
  await ui.click('＋ 添加音频类型');ui.select();await ui.click('保存设置');
  assert.equal(ui.writes[1].expected_revision,0);
  assert.equal(ui.cards[0].label.textContent,'已开启 · 1项');
});

test('descriptions are text in both picker and selected rows',async()=>{
 const ui=await setup(); await ui.click('＋ 添加音频类型');
 assert.ok(all(ui.body).some(n=>n.className==='recognition-description'&&n.textContent==='说明0<b>原样文本</b>'));
 ui.select(); await ui.click('添加所选');
 assert.ok(all(ui.body).some(n=>n.className==='recognition-description'&&n.textContent==='说明0<b>原样文本</b>'));
});
