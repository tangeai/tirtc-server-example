const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const { buildDeviceWebPage } = require('../utils/device-web-page')

test('固定 HTTPS 页面及编码参数，凭证仅在 fragment 中传递', () => {
  for (const [kind, pathname] of [['live', '/player'], ['agent', '/v1/ai/agent']]) {
    const page = buildDeviceWebPage('https://app.example.com/', kind, '设备 &/#', 'test-token')
    const url = new URL(page.url)
    assert.equal(url.pathname, pathname)
    assert.equal(url.searchParams.get('device_id'), '设备 &/#')
    assert.equal(url.searchParams.get('source'), 'miniprogram')
    assert.equal(url.search.includes('test-token'), false)
    assert.equal(url.hash, '#mini_token=test-token')
  }
})

test('拒绝任意地址、用户信息、非 HTTPS 地址和未登录入口', () => {
  for (const base of ['http://a.com', 'https://a.com@evil.com', 'https://a.com/path',
    'https://a.com?next=evil', 'https://a.com#x', 'https://a.com\\evil.com', '//evil.com']) {
    assert.throws(() => buildDeviceWebPage(base, 'live', 'device', 'token'))
  }
  for (const kind of ['https://evil.com', 'constructor', '__proto__', 'unknown']) {
    assert.throws(() => buildDeviceWebPage('https://a.com', kind, 'device', 'token'))
  }
  assert.throws(() => buildDeviceWebPage('https://a.com', 'live', '', 'token'))
  assert.throws(() => buildDeviceWebPage('https://a.com', 'live', 'device', ''))
})

function embeddedPage(apiResult, token = 'native-token') {
  let definition
  const calls = []
  const wx = {
    getStorageSync: () => token,
    removeStorageSync: key => calls.push(['remove', key]),
    reLaunch: options => calls.push(['login', options.url]),
    hideShareMenu() {}, setNavigationBarTitle() {},
    showNavigationBarLoading() {}, hideNavigationBarLoading() {},
  }
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../pages/device-web/index.js'), 'utf8'), {
    Page: value => { definition = value },
    getApp: () => ({ globalData: { userServerBaseUrl: 'https://app.example.com' } }),
    require: id => id.includes('device-web-page') ? { buildDeviceWebPage } : { userApi: async () => {
      if (apiResult instanceof Error) throw apiResult
      return apiResult
    } },
    wx, setTimeout: () => 1, clearTimeout() {},
  })
  const page = { ...definition, data: { ...definition.data }, setData(value) { Object.assign(this.data, value) } }
  page._options = { kind: 'live', deviceId: 'one' }
  return { page, calls }
}

test('二次验证所有权和在线状态，离线可编辑 AI 角色', async () => {
  const { page } = embeddedPage({ code: 200, data: [{ device_id: 'one', online: false }] })
  await page.openPage()
  assert.equal(page.data.src, '')
  assert.match(page.data.error, /离线/)
  page._options.kind = 'agent'
  await page.openPage()
  assert.match(page.data.src, /\/v1\/ai\/agent\?/)
  page.onWebError()
  assert.equal(page.data.src, '')
  assert.equal(page.data.loading, false)
  assert.match(page.data.error, /重试/)
})

test('解绑设备不会获得页面地址', async () => {
  const { page } = embeddedPage({ code: 200, data: [] })
  await page.openPage()
  assert.equal(page.data.src, '')
  assert.match(page.data.error, /解绑/)
})

test('HTTP 401 和缺失登录态都返回小程序登录页', async () => {
  for (const token of ['', 'expired']) {
    const { page, calls } = embeddedPage(Object.assign(new Error(), { code: 401 }), token)
    await page.openPage()
    assert.equal(page.data.src, '')
    assert.ok(calls.some(c => c[0] === 'login'))
  }
})

test('页面退出后忽略迟到的设备响应', async () => {
  const { page } = embeddedPage({ code: 200, data: [{ device_id: 'one', online: true }] })
  const pending = page.openPage()
  page.onUnload()
  await pending
  assert.equal(page.data.src, '')
})

test('H5 登录隔离：先清理片段，内嵌页面不读取或改写浏览器账号', () => {
  const source = fs.readFileSync(path.join(__dirname, '../../user-server/static/js/mini-program-page.js'), 'utf8')
  for (const embedded of [true, false]) {
    const calls = []
    const window = {}
    const context = {
      window, URLSearchParams,
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
