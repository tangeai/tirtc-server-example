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
  const calls = {
    connects: 0, attaches: 0, disconnects: 0, talks: 0, stops: 0,
    logins: 0, subscribeAudio: [], subscribeVideo: [],
    unsubscribeAudio: [], unsubscribeVideo: [],
  }
  const connectionReady = options.connectionReady || Promise.resolve()
  const talkReady = options.talkReady || Promise.resolve()
  const style = () => ({ setProperty(name, value) { this[name] = value } })
  const document = {
    hidden: false,
    getElementById(id) {
      if (!elements.has(id)) elements.set(id, { style: style(), classList: { add() {}, remove() {}, toggle() {} } })
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
    subscribeVideo({ streamId }) { calls.subscribeVideo.push(streamId) }
    subscribeAudio({ streamId }) { calls.subscribeAudio.push(streamId) }
    unsubscribeVideo({ streamId }) { calls.unsubscribeVideo.push(streamId) }
    unsubscribeAudio({ streamId }) { calls.unsubscribeAudio.push(streamId) }
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

test('连接中的页面隐藏不会释放会话，连接完成后继续播放', async () => {
  const ready = deferred()
  const p = player({ connectionReady: ready.promise })
  await tick()
  assert.equal(p.calls.connects, 1)
  p.document.hidden = true
  if (p.listeners.visibilitychange) p.listeners.visibilitychange()
  ready.resolve()
  await tick()
  assert.equal(p.calls.disconnects, 0)
  assert.equal(p.calls.attaches, 1)
  assert.deepEqual(p.calls.subscribeVideo, [11])
})

test('页面隐藏只停止对讲，不退订或断开实时查看', async () => {
  const p = player()
  await tick()
  p.window.startTalk()
  await tick()
  assert.equal(p.calls.talks, 1)

  p.document.hidden = true
  p.listeners.visibilitychange()

  assert.ok(p.calls.stops > 0)
  assert.equal(p.calls.disconnects, 0)
  assert.deepEqual(p.calls.unsubscribeAudio, [])
  assert.deepEqual(p.calls.unsubscribeVideo, [])
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

test('默认静音，用户点击后才挂载并订阅音频', async () => {
  const p = player()
  await tick()
  assert.deepEqual(p.calls.subscribeAudio, [])
  assert.deepEqual(p.calls.subscribeVideo, [11])

  p.window.toggleMute()
  assert.deepEqual(p.calls.subscribeAudio, [10])

  p.window.toggleMute()
  assert.deepEqual(p.calls.unsubscribeAudio, [10])

  p.window.toggleMute()
  assert.deepEqual(p.calls.subscribeAudio, [10, 10])

  p.listeners.pagehide()
  assert.deepEqual(p.calls.unsubscribeAudio, [10, 10])
  assert.deepEqual(p.calls.unsubscribeVideo, [11])
})

test('连接失败提供可重试状态，不遗留连接', async () => {
  const p = player({ fetchResult: Promise.resolve({ json: async () => ({ code: 401 }) }) })
  await tick()
  assert.equal(p.calls.logins, 1)
  assert.equal(p.elements.get('player-retry').hidden, false)
  assert.equal(p.calls.connects, 0)
})

test('设备未上报呈现属性时使用默认画面配置', async () => {
  const p = player()
  await tick()
  const boxStyle = p.elements.get('canvas-box').style
  const canvasStyle = p.elements.get('canvas').style
  assert.equal(Number(boxStyle['--video-ratio']), 16 / 9)
  assert.equal(canvasStyle.objectFit, 'contain')
  assert.equal(canvasStyle.width, '100%')
  assert.equal(canvasStyle.height, '100%')
  assert.equal(canvasStyle.transform, 'translate(-50%, -50%)')
})

test('设备上报的窄幅比例不会被改写', async () => {
  const p = player({ fetchResult: Promise.resolve({ json: async () => ({
    code: 200,
    data: { token: 'rtc', app_id: 'one', profiles: { stream: { aspect_ratio: '1:10' } } },
  }) }) })
  await tick()
  assert.equal(p.elements.get('canvas-box').style['--video-ratio'], '0.1')
  assert.equal(p.elements.get('canvas').style.width, '100%')
  assert.equal(p.elements.get('canvas').style.height, '100%')
})

test('旋转九十度时交换画布尺寸并保持上报比例', async () => {
  const p = player({ fetchResult: Promise.resolve({ json: async () => ({
    code: 200,
    data: { token: 'rtc', app_id: 'one', profiles: { stream: {
      aspect_ratio: '4:3', camera_rotation: 90, object_fit: 'cover', hor_mirror: true,
    } } },
  }) }) })
  await tick()
  const boxStyle = p.elements.get('canvas-box').style
  const canvasStyle = p.elements.get('canvas').style
  assert.equal(Number(boxStyle['--video-ratio']), 0.75)
  assert.equal(Number.parseFloat(canvasStyle.width), 100 / 0.75)
  assert.equal(Number.parseFloat(canvasStyle.height), 75)
  assert.equal(canvasStyle.objectFit, 'cover')
  assert.equal(canvasStyle.transform, 'translate(-50%, -50%) rotate(90deg) scaleX(-1)')
})
