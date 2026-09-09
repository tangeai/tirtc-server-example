const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const {webcrypto} = require('node:crypto');
const html = fs.readFileSync(require('node:path').join(__dirname, '../handler/room_page.html'), 'utf8');
const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];

async function page(crypto, respond = async () => ({code: 200, data: {desired_state: 'joined', state: 'waiting_device', room_code: '001234'}})) {
  const nodes = new Map();
  const node = id => {
    if (!nodes.has(id)) nodes.set(id, {textContent: '', value: '', hidden: false, disabled: false, style: {}, focus() {}, showModal() {}, close() {}});
    return nodes.get(id);
  };
  const buttons = [{disabled: false}, {disabled: false}];
  const posts = [];
  const context = vm.createContext({
    document: {getElementById: node, querySelectorAll: selector => selector === 'button, input' ? buttons : []},
    location: {search: '?device_id=test-device'},
    localStorage: {getItem: () => 'test-token'},
    URLSearchParams, AbortSignal, Uint8Array, crypto,
    setInterval() {}, clearInterval() {}, addEventListener() {},
    fetch: async (url, options) => {
      if (options.method === 'POST') {
        posts.push({url, ...options});
        return {status: 200, json: async () => respond()};
      }
      return {status: 200, json: async () => ({code: 200, data: url.endsWith('/list') ? [{device_id: 'test-device'}] : {desired_state: 'left'}})};
    },
  });
  vm.runInContext(script, context);
  await new Promise(resolve => setImmediate(resolve));
  return {node, buttons, posts, change: (kind = 'create', body = {password: ''}) => context.change(kind, body)};
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
