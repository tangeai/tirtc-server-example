const app = getApp()

function request(baseUrl, path, method = 'GET', data, extraHeaders = {}) {
  return new Promise((resolve, reject) => {
    wx.request({
      url: baseUrl + path,
      method,
      timeout: 15000,
      header: { 'content-type': 'application/json', ...extraHeaders },
      data,
      success(res) {
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
