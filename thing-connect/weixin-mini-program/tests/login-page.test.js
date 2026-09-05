const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')

function setup(provider, component = { popUp() {} }) {
  let definition
  const calls = []
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../pages/login/index.js'), 'utf8'), {
    Page: value => { definition = value },
    require: id => id.endsWith('/session') ? {
      clearSession: () => calls.push('clear'), showExpiredToast: () => calls.push('toast'),
    } : { userApi: async () => ({ data: { enabled: true, provider, captcha_id: 'test-id' } }) },
    wx: { getStorageSync: () => '', redirectTo: () => calls.push('redirect') },
  })
  const page = { ...definition, data: { ...definition.data, loginEmail: 'a@example.com', loginPassword: 'password', regEmail: 'a@example.com' },
    setData(value) { Object.assign(this.data, value) }, selectComponent: () => component,
    _submitLogin: () => calls.push('login'), _submitSendCode: () => calls.push('send'),
  }
  return { page, calls }
}

test('网易验证同时用于登录和注册发送验证码', async () => {
  let opened = 0
  const { page, calls } = setup('yidun', { popUp: () => { opened++ } })
  await page._loadCaptchaId()
  page.doLogin()
  page.doSendCode()
  assert.equal(opened, 2)
  assert.equal(calls.length, 0)
  page.onLoginCaptchaVerify({ detail: [null, 'test-validation'] })
  page.onRegCaptchaVerify({ detail: [null, 'test-validation'] })
  assert.deepEqual(calls, ['login', 'send'])
})

test('其他验证码服务或网易组件未加载时不能跳过验证', async () => {
  for (const provider of ['tencent', 'geetest', 'aliyun', 'yidun']) {
    const { page, calls } = setup(provider, null)
    await page._loadCaptchaId()
    page.doLogin()
    page.doSendCode()
    assert.equal(calls.length, 0)
    assert.ok(page.data.errMsg)
  }
})

test('未启用人机验证时可直接提交', async () => {
  const { page, calls } = setup('')
  page.doLogin()
  page.doSendCode()
  assert.deepEqual(calls, ['login', 'send'])
})

test('H5 过期返回首页时清除原登录态，并在显示首页时提示', () => {
  const { page, calls } = setup('yidun')
  page.onLoad({ expired: '1' })
  assert.deepEqual(calls, ['clear'])
  page.onShow()
  assert.deepEqual(calls, ['clear', 'toast'])
})
