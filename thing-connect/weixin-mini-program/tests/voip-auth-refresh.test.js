const assert = require('node:assert/strict')
const test = require('node:test')

const { refreshVoipAuthState } = require('../utils/voip-auth-refresh')

function taskSet(options = {}) {
  const calls = {
    login: 0,
    wechatAuth: 0,
    remark: 0,
    authList: 0,
    reconcile: 0,
  }
  return {
    calls,
    tasks: {
      async refreshOpenId() {
        calls.login += 1
        if (options.loginFails) throw new Error('login failed')
      },
      async syncWechatAuthState() {
        calls.wechatAuth += 1
      },
      async syncContactRemark() {
        calls.remark += 1
      },
      async syncServerAuthList() {
        calls.authList += 1
        return options.authListLoaded !== false
      },
      async reconcileAuthorizedDevices() {
        calls.reconcile += 1
        return !!options.authChanged
      },
    },
  }
}

test('普通下拉只登录一次并读取一次 auth-list', async () => {
  const { calls, tasks } = taskSet()

  const result = await refreshVoipAuthState(tasks)

  assert.deepEqual(calls, {
    login: 1,
    wechatAuth: 1,
    remark: 1,
    authList: 1,
    reconcile: 1,
  })
  assert.deepEqual(result, {
    serverAuthListLoaded: true,
    serverAuthChanged: false,
  })
})

test('授权对账确实写入后才第二次读取 auth-list', async () => {
  const { calls, tasks } = taskSet({ authChanged: true })

  const result = await refreshVoipAuthState(tasks)

  assert.equal(calls.login, 1)
  assert.equal(calls.authList, 2)
  assert.equal(calls.reconcile, 1)
  assert.deepEqual(result, {
    serverAuthListLoaded: true,
    serverAuthChanged: true,
  })
})

test('登录刷新失败只同步微信本地状态且不重复登录', async () => {
  const { calls, tasks } = taskSet({
    loginFails: true,
    authListLoaded: false,
  })

  const result = await refreshVoipAuthState(tasks)

  assert.equal(calls.login, 1)
  assert.equal(calls.wechatAuth, 1)
  assert.equal(calls.remark, 0)
  assert.equal(calls.authList, 0)
  assert.equal(calls.reconcile, 0)
  assert.deepEqual(result, {
    serverAuthListLoaded: false,
    serverAuthChanged: false,
  })
})
