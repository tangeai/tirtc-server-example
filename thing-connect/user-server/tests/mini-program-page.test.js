const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const languageRuntime = require('../../tests/helpers/web-i18n.cjs')
test('H5 过期等待微信 SDK 就绪后自动返回登录首页，并去重', () => {
  const source = fs.readFileSync(path.join(__dirname, '../static/js/mini-program-page.js'), 'utf8')
  const navigations = []
  let sdk, panel
  const window = {}
  vm.runInNewContext(source, {
    window, URLSearchParams, WebI18n: languageRuntime(),
    location: { search: '?source=miniprogram', hash: '#mini_token=test-token', pathname: '/player' },
    history: { replaceState() {} },
    document: {
      documentElement: { classList: { add() {} } },
      getElementById: () => panel,
      createElement: () => ({ style: {}, append() {} }),
      head: { appendChild: value => { sdk = value } },
      body: { appendChild: value => { panel = value } },
    },
  })
  window.MiniProgramPage.requireLogin()
  assert.equal(navigations.length, 0)
  window.wx = { miniProgram: { reLaunch: options => navigations.push(options) } }
  sdk.onload()
  window.MiniProgramPage.requireLogin()
  assert.equal(navigations.length, 1)
  assert.equal(navigations[0].url, '/pages/login/index?expired=1')
  assert.equal(navigations[0].url.includes('test-token'), false)
})

test('H5 登录隔离：先清理片段，内嵌页面不读取或改写浏览器账号', () => {
  const source = fs.readFileSync(path.join(__dirname, '../static/js/mini-program-page.js'), 'utf8')
  for (const embedded of [true, false]) {
    const calls = []
    const window = {}
    const context = {
      window, URLSearchParams, WebI18n: languageRuntime(),
      location: { search: embedded ? '?source=miniprogram&device_id=one' : '?device_id=one',
        hash: '#mini_token=native-token', pathname: '/player' },
      history: { replaceState: (_, __, url) => calls.push(['clear', url]) },
      localStorage: { getItem: () => { calls.push(['read']); return 'browser-token' } },
      document: { documentElement: { classList: { add() {} } },
        createElement: () => ({}), head: { appendChild: () => calls.push(['sdk']) } },
    }
    vm.runInNewContext(source, context)
    assert.equal(calls[0][0], 'clear')
    assert.equal(window.MiniProgramPage.token, embedded ? 'native-token' : 'browser-token')
    assert.equal(calls.some(c => c[0] === 'read'), !embedded)
  }
})
