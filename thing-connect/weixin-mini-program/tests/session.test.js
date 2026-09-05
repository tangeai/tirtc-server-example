const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')

function setup() {
  let token = 'old-token'
  const requests = [], navigations = [], toasts = []
  const app = { globalData: { wxOpenId: 'old-openid', userServerBaseUrl: 'https://example.com', voipServerBaseUrl: 'https://example.com' } }
  const wx = {
    getStorageSync: () => token,
    removeStorageSync: () => { token = '' },
    request: options => requests.push(options),
    reLaunch: options => navigations.push(options),
    showToast: options => toasts.push(options),
  }
  function load(file, dependencies = {}) {
    const module = { exports: {} }
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, '..', file), 'utf8'), {
      module, wx, getApp: () => app, require: id => dependencies[id],
    })
    return module.exports
  }
  const session = load('utils/session.js')
  const api = load('utils/api.js', { './session': session })
  return { session, api, requests, navigations, toasts, app, setToken: value => { token = value }, token: () => token }
}

test('并发过期只返回一次首页，清除 OpenID，到达首页后再提示', async () => {
  const h = setup()
  const a = h.api.userApi('/v1/user/device/list').catch(e => e)
  const b = h.api.voipApi('/v1/voip/user/auth-list').catch(e => e)
  h.requests.forEach(r => r.success({ statusCode: 401, data: { code: 401 } }))
  assert.equal((await a).sessionExpired, true)
  assert.equal((await b).sessionExpired, true)
  assert.equal(h.navigations.length, 1)
  assert.equal(h.navigations[0].url, '/pages/login/index')
  assert.equal(h.token(), '')
  assert.equal(h.app.globalData.wxOpenId, '')
  assert.equal(h.toasts.length, 0)
  h.session.showExpiredToast()
  h.session.showExpiredToast()
  assert.equal(h.toasts.length, 1)
  assert.equal(h.toasts[0].title, '登录状态已过期，请重新登录')
  h.navigations[0].complete()
  h.session.expireSession('')
  assert.equal(h.navigations.length, 1)
})

test('支持 HTTP 200 中的业务 401，以及缺少响应体的 HTTP 401', async () => {
  for (const response of [{ statusCode: 200, data: { code: 401 } }, { statusCode: 401 }]) {
    const h = setup()
    const result = h.api.userApi('/v1/user/device/list').catch(e => e)
    h.requests[0].success(response)
    assert.equal((await result).sessionExpired, true)
    assert.equal(h.navigations.length, 1)
  }
})

test('旧账号迟到的 401 不清除新账号登录态', async () => {
  const h = setup()
  const result = h.api.userApi('/v1/user/device/list').catch(e => e)
  h.setToken('new-token')
  h.requests[0].success({ statusCode: 401, data: { code: 401 } })
  await result
  assert.equal(h.token(), 'new-token')
  assert.equal(h.navigations.length, 0)
})

test('密码错误、微信授权失效和网络错误不触发退出', async () => {
  for (const [url, response] of [
    ['/v1/user/login', { statusCode: 401, data: { code: 4091 } }],
    ['/v1/user/login', { statusCode: 401, data: { code: 401 } }],
    ['/v1/voip/user/auth-list', { statusCode: 401, data: { code: 40205 } }],
    ['/v1/user/device/list', null],
  ]) {
    const h = setup()
    const result = h.api.userApi(url).catch(e => e)
    if (response) h.requests[0].success(response)
    else h.requests[0].fail()
    await result
    assert.equal(h.navigations.length, 0)
    assert.equal(h.token(), 'old-token')
  }
})

test('重新登录后再次过期仍可跳转', async () => {
  const h = setup()
  h.session.expireSession('old-token')
  h.navigations[0].complete()
  h.session.showExpiredToast()
  h.setToken('new-token')
  h.session.expireSession('new-token')
  assert.equal(h.navigations.length, 2)
})
