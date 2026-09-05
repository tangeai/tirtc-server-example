const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const { createRequire } = require('node:module')

function makePage(result) {
  let definition
  const filename = path.join(__dirname, '../pages/devices/index.js')
  const realRequire = createRequire(filename)
  const calls = []
  vm.runInNewContext(fs.readFileSync(filename, 'utf8'), {
    Page: value => { definition = value },
    getApp: () => ({ globalData: {} }),
    require: id => {
      if (id.endsWith('/session')) return { expireSession: () => calls.push(['login']) }
      if (id.endsWith('/api')) return { userApi: async () => {
        if (result instanceof Error) throw result
        return result
      } }
      if (id.endsWith('/voip-auth-refresh')) return { refreshVoipAuthState: async () => {} }
      if (id.endsWith('/voip-video-profile')) return {
        ...realRequire(id), updateDeviceVideoProfileCache() {},
      }
      return realRequire(id)
    },
    wx: {
      getStorageSync: () => 'test', removeStorageSync() {},
      showNavigationBarLoading() {}, hideNavigationBarLoading() {}, stopPullDownRefresh() {},
      showToast: value => calls.push(['toast', value.title]),
      reLaunch: value => calls.push(['login', value.url]),
      navigateTo: value => calls.push(['navigate', value.url]),
      setClipboardData: value => calls.push(['copy', value.data]),
    },
  })
  const page = { ...definition, data: { ...definition.data }, setData(value) { Object.assign(this.data, value) } }
  return { page, calls }
}

test('首次网络失败显示重试状态，不误报暂无设备', async () => {
  const { page } = makePage(new Error('network'))
  await page.loadDevices()
  assert.ok(page.data.loadError)
  assert.equal(page.data.initialized, true)
  assert.equal(page.data.loading, false)
})

test('刷新失败保留设备，登录过期则返回登录页', async () => {
  const { page } = makePage(new Error('network'))
  page.data.deviceList = [{ device_id: 'one' }]
  await page.loadDevices()
  assert.equal(page.data.deviceList.length, 1)
  const expired = makePage(Object.assign(new Error(), { code: 401 }))
  await expired.page.loadDevices()
  assert.ok(expired.calls.some(c => c[0] === 'login'))
})

test('实时入口按在线状态限制，AI 入口离线可用，导航不含凭证', () => {
  const { page, calls } = makePage()
  page.data.deviceList = [{ device_id: 'one&two', online: false }]
  page.onOpenLive({ currentTarget: { dataset: { id: 'one&two' } } })
  assert.equal(calls.some(c => c[0] === 'navigate'), false)
  page.onOpenAgent({ currentTarget: { dataset: { id: 'one&two' } } })
  const url = calls.find(c => c[0] === 'navigate')[1]
  assert.match(url, /kind=agent&device_id=one%26two/)
  assert.equal(url.includes('token'), false)
})

test('销毁列表页后，迟到结果不能更新视图', async () => {
  const { page } = makePage({ code: 200, data: [{ device_id: 'one' }] })
  const pending = page.loadDevices()
  page.onUnload()
  await pending
  assert.equal(page.data.deviceList.length, 0)
})

test('联系人设置展示 OpenID 并复制完整值', async () => {
  const { page, calls } = makePage()
  page._fetchOpenId = async () => 'test-openid-complete'
  await page.onEditContactRemark()
  assert.equal(page.data.authNameMode, 'profile')
  assert.equal(page.data.profileOpenId, 'test-openid-complete')
  page.onCopyProfileOpenId()
  assert.deepEqual(calls.find(c => c[0] === 'copy'), ['copy', 'test-openid-complete'])
})

test('OpenID 获取失败可重试，关闭弹窗后忽略迟到结果', async () => {
  const { page } = makePage()
  page._fetchOpenId = async () => { throw new Error('network') }
  await page.onEditContactRemark()
  assert.equal(page.data.profileOpenIdError, true)
  page._fetchOpenId = async () => 'test-retry-openid'
  await page.loadProfileOpenId()
  assert.equal(page.data.profileOpenId, 'test-retry-openid')
  assert.equal(page.data.profileOpenIdError, false)
  let resolve
  page._fetchOpenId = () => new Promise(r => { resolve = r })
  const pending = page.loadProfileOpenId()
  page.onCancelAuthName()
  resolve('test-late-openid')
  await pending
  assert.notEqual(page.data.profileOpenId, 'test-late-openid')
})
