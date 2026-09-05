const app = getApp()
const { expireSession } = require('./session')
const PUBLIC_PATHS = new Set(['/v1/config/captcha', '/v1/user/login', '/v1/user/register', '/v1/user/send-code'])

function request(baseUrl, path, method = 'GET', data, extraHeaders = {}) {
  return new Promise((resolve, reject) => {
    wx.request({
      url: baseUrl + path,
      method,
      timeout: 15000,
      header: { 'content-type': 'application/json', ...extraHeaders },
      data,
      success(res) {
        const code = res.data && res.data.code
        if (!PUBLIC_PATHS.has(path) && (code === 401 || (res.statusCode === 401 && code == null))) {
          const token = (extraHeaders.Authorization || '').replace(/^Bearer /, '')
          expireSession(token)
          reject({ code: 401, msg: '登录状态已过期，请重新登录', sessionExpired: true })
          return
        }
        if (res.statusCode === 200) {
          resolve(res.data)
        } else {
          reject({ code: (res.data && res.data.code) || res.statusCode, msg: (res.data && res.data.msg) || '请求失败' })
        }
      },
      fail() {
        reject({ code: -1, msg: '网络连接失败，请检查网络后重试' })
      },
    })
  })
}

function userApi(path, method = 'GET', data) {
  const token = wx.getStorageSync('token') || ''
  return request(
    app.globalData.userServerBaseUrl,
    path,
    method,
    data,
    token ? { Authorization: 'Bearer ' + token } : {}
  )
}

function voipApi(path, method = 'POST', data) {
  const token = wx.getStorageSync('token') || ''
  return request(
    app.globalData.voipServerBaseUrl,
    path,
    method,
    data,
    token ? { Authorization: 'Bearer ' + token } : {}
  )
}

module.exports = { userApi, voipApi }
