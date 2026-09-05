const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')

function deferred() {
  let resolve
  const promise = new Promise(r => { resolve = r })
  return { promise, resolve }
}
const tick = () => new Promise(resolve => setImmediate(resolve))

function player(options = {}) {
  const html = fs.readFileSync(path.join(__dirname, '../../user-server/static/player.html'), 'utf8')
  const script = html.match(/<script type="module">([\s\S]*?)<\/script>/)[1].replace(/^import .*;$/m, '')
  const elements = new Map()
  const listeners = {}
  const calls = { connects: 0, attaches: 0, disconnects: 0, talks: 0, stops: 0, logins: 0 }
  const connectionReady = options.connectionReady || Promise.resolve()
  const talkReady = options.talkReady || Promise.resolve()
  const document = {
    hidden: false,
    getElementById(id) {
      if (!elements.has(id)) elements.set(id, { style: {}, classList: { add() {}, remove() {}, toggle() {} } })
      return elements.get(id)
    },
    addEventListener: (name, fn) => { listeners[name] = fn },
  }
  const window = {
    MiniProgramPage: { token: 'test', requireLogin: () => { calls.logins++ } },
    addEventListener: (name, fn) => { listeners[name] = fn },
  }
  const output = () => ({ attach() { calls.attaches++ }, detach() {} })
  class Input {
    setOptions() {}
    start() { return talkReady }
    attach() { calls.talks++; return Promise.resolve() }
    stop() { calls.stops++ }
    detach() { return Promise.resolve() }
  }
  class Connection {
    connect() { calls.connects++; return connectionReady }
    disconnect() { calls.disconnects++ }
    subscribeVideo() {}
    subscribeAudio() {}
  }
  const context = {
    window, document, URLSearchParams, AbortController,
    setTimeout: () => 1, clearTimeout() {},
    location: { search: '?device_id=one' },
    fetch: () => options.fetchResult || Promise.resolve({ json: async () => ({ code: 200, data: { token: 'rtc', app_id: 'one' } }) }),
    TiRtc: { initialize() {}, videoOutputReady: async () => {} },
    TiRtcInitOptions: v => v, TiRtcConn: Connection, TiRtcAudioInput: Input,
    TiRtcAudioOutput: output, TiRtcVideoOutput: output,
  }
  vm.runInNewContext(script, context)
  return { window, document, calls, listeners, elements }
}

test('离开页面后迟到的 token 响应不能重新建立媒体连接', async () => {
  const response = deferred()
  const p = player({ fetchResult: response.promise })
  p.listeners.pagehide()
  response.resolve({ json: async () => ({ code: 200, data: { token: 'late', app_id: 'one' } }) })
  await tick()
  assert.equal(p.calls.connects, 0)
})

test('连接中的页面隐藏会释放会话，迟到连接成功不恢复播放', async () => {
  const ready = deferred()
  const p = player({ connectionReady: ready.promise })
  await tick()
  assert.equal(p.calls.connects, 1)
  p.document.hidden = true
  p.listeners.visibilitychange()
  ready.resolve()
  await tick()
  assert.equal(p.calls.disconnects, 1)
  assert.equal(p.calls.attaches, 0)
  assert.match(p.elements.get('player-message').textContent, /暂停/)
})

test('麦克风权限迟到返回时，已松开的按键不能继续上行音频', async () => {
  const ready = deferred()
  const p = player({ talkReady: ready.promise })
  await tick()
  p.window.startTalk()
  p.window.stopTalk()
  ready.resolve()
  await tick()
  assert.equal(p.calls.talks, 0)
  assert.ok(p.calls.stops > 0)
})

test('连接失败提供可重试状态，不遗留连接', async () => {
  const p = player({ fetchResult: Promise.resolve({ json: async () => ({ code: 401 }) }) })
  await tick()
  assert.equal(p.calls.logins, 1)
  assert.equal(p.elements.get('player-retry').hidden, false)
  assert.equal(p.calls.connects, 0)
})
