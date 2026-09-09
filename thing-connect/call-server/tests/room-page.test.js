const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const {webcrypto} = require('node:crypto');
const html = fs.readFileSync(require('node:path').join(__dirname, '../handler/room_page.html'), 'utf8');
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];

async function page(crypto, respond = async () => ({code: 200, data: {desired_state: 'joined', state: 'waiting_device', room_code: '001234'}}), readCurrent = null) {
  const nodes = new Map();
  const node = id => {
    if (!nodes.has(id)) nodes.set(id, {textContent: '', value: '', hidden: false, disabled: false, style: {}, focus() {}, showModal() {}, close() {}});
    return nodes.get(id);
  };
  const buttons = [{disabled: false}, {disabled: false}];
  const posts = [];
  let latest = {desired_state: 'left'};
  let reads = 0;
  const context = vm.createContext({
    document: {getElementById: node, querySelectorAll: selector => selector === 'button, input' ? buttons : []},
    location: {search: '?device_id=test-device'},
    localStorage: {getItem: () => 'test-token'},
    URLSearchParams, AbortSignal, Uint8Array, crypto,
    setInterval() {}, clearInterval() {}, addEventListener() {},
    fetch: async (url, options) => {
      if (options.method === 'POST') {
        posts.push({url, ...options});
        return {status: 200, json: async () => {const body = await respond(); if(body.code === 200) latest = body.data; return body;}};
      }
      if (url.endsWith('/list')) return {status:200, json:async()=>({code:200,data:[{device_id:'test-device'}]})};
      reads++;
      return {status:200, json:async()=>({code:200,data:readCurrent ? await readCurrent(posts.length) : latest})};
    },
  });
  vm.runInContext(script, context);
  await new Promise(resolve => setImmediate(resolve));
  return {node, buttons, posts, reads: () => reads, change: (kind = 'create', body = {password: ''}) => context.change(kind, body)};
}

const httpCrypto = () => ({getRandomValues: array => webcrypto.getRandomValues(array)});

test('HTTP create sends a request and displays the assigned room', async () => {
  const p = await page(httpCrypto());
  await p.change();
  assert.equal(p.posts.length, 1);
  assert.equal(p.posts[0].url, '/v1/call/room/web/device/test-device/create');
  assert.match(p.posts[0].headers['Idempotency-Key'], /^[a-f0-9-]{32,36}$/);
  assert.equal(p.node('code').textContent, '001234');
  assert.equal(p.node('current').hidden, false);
  assert.equal(p.node('message').textContent, '');
  assert.ok(p.buttons.every(b => !b.disabled));
});

test('HTTPS create retains native UUID support', async () => {
  const p = await page(webcrypto);
  await p.change();
  assert.match(p.posts[0].headers['Idempotency-Key'], /^[a-f0-9-]{36}$/);
});

test('request preparation failure is visible and a later click can retry', async () => {
  const crypto = httpCrypto();
  crypto.getRandomValues = () => {throw Error('随机数生成失败');};
  const p = await page(crypto);
  await p.change();
  assert.match(p.node('message').textContent, /随机数生成失败/);
  assert.equal(p.posts.length, 0);
  assert.ok(p.buttons.every(b => !b.disabled));
  crypto.getRandomValues = array => webcrypto.getRandomValues(array);
  await p.change();
  assert.equal(p.posts.length, 1);
});

test('ambiguous network failure reuses the key; next operation gets a new key', async () => {
  let attempts = 0;
  const p = await page(httpCrypto(), async () => {
    if (++attempts === 1) throw Error('网络中断');
    return {code: 200, data: {desired_state: 'joined', room_code: '001234'}};
  });
  await p.change();
  assert.match(p.node('message').textContent, /网络中断/);
  await p.change();
  assert.equal(p.posts[0].headers['Idempotency-Key'], p.posts[1].headers['Idempotency-Key']);
  await p.change('join', {room_code: '654321', password: ''});
  assert.notEqual(p.posts[1].headers['Idempotency-Key'], p.posts[2].headers['Idempotency-Key']);
});

test('submission blocks duplicate clicks and clearly reports success', async () => {
  let resolve;
  const response = new Promise(done => {resolve = done;});
  const p = await page(httpCrypto(), () => response);
  const first = p.change();
  assert.equal(p.node('createSubmit').textContent, '正在创建…');
  assert.ok(p.buttons.every(b => b.disabled));
  await p.change();
  assert.equal(p.posts.length, 1);
  resolve({code: 200, data: {desired_state: 'joined', room_code: '001234'}});
  await first;
  assert.match(p.node('successMessage').textContent, /房间已创建/);
  assert.equal(p.node('successMessage').hidden, false);
});

test('business rejection preserves entered password and shows the reason', async () => {
  const p = await page(httpCrypto(), () => ({code: 40320, msg: '房间密码错误，请重新输入'}));
  p.node('joinPassword').value = '1234';
  await p.change('join', {room_code: '001234', password: '1234'});
  assert.match(p.node('message').textContent, /房间密码错误/);
  assert.equal(p.node('message').hidden, false);
  assert.equal(p.node('joinPassword').value, '1234');
  assert.ok(p.buttons.every(b => !b.disabled));
});


test('joining reads authoritative room counts instead of command defaults without polling', async () => {
  const p = await page(httpCrypto(), async () => ({code:200,data:{desired_state:'joined',state:'assigned',room_code:'668740',online:false,online_count:0}}), async posts => posts ? {desired_state:'joined',state:'joined',room_code:'668740',online:true,online_count:2} : {desired_state:'left'});
  await p.change('join', {room_code:'668740',password:''});
  assert.equal(p.node('count').textContent,'在线设备：2');
  assert.equal(p.node('deviceOnline').textContent,'● 在线');
  assert.equal(p.node('state').textContent,'已加入');
  assert.equal(p.posts.length,1);
  assert.equal(p.reads(),2);
});

test('successful command with failed status read preserves success and never invents zero participants', async () => {
  const p = await page(httpCrypto(), async () => ({code:200,data:{desired_state:'joined',state:'assigned',room_code:'668740',online:false,online_count:0}}), async posts => {if(posts)throw Error('network');return {desired_state:'left'};});
  await p.change('join',{room_code:'668740',password:''});
  assert.equal(p.node('count').textContent,'在线设备：待更新');
  assert.equal(p.node('deviceOnline').textContent,'状态待更新');
  assert.equal(p.node('successMessage').hidden,false);
  assert.match(p.node('syncMessage').textContent,/状态更新失败/);
  assert.equal(p.node('syncMessage').hidden,false);
  assert.equal(p.posts.length,1);
  assert.ok(p.buttons.every(b=>!b.disabled));
});
