const app = getApp()

function request(baseUrl, path, method = 'GET', data, extraHeaders = {}) {
  return new Promise((resolve, reject) => {
    wx.request({
      url: baseUrl + path,
      method,
      header: { 'content-type': 'application/json', ...extraHeaders },
      data,
      success(res) {
        if (res.statusCode === 200) {
          resolve(res.data)
        } else {
          reject({ code: res.statusCode, msg: (res.data && res.data.msg) || '请求失败' })
        }
      },
      fail(err) {
        reject({ code: -1, msg: err.errMsg || '网络错误' })
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
