const EXPIRED_MESSAGE = '登录状态已过期，请重新登录'
let pendingToast = false
let redirecting = false
let signedOut = false

function clearSession() {
  wx.removeStorageSync('token')
  const app = getApp()
  app.globalData.wxOpenId = ''
  pendingToast = true
  signedOut = true
}

function expireSession(requestToken) {
  // A late response from an old account must not sign out the current account.
  const currentToken = wx.getStorageSync('token') || ''
  if (requestToken !== undefined && requestToken !== currentToken) return
  if (redirecting || (!currentToken && signedOut)) return
  redirecting = true
  clearSession()
  wx.reLaunch({
    url: '/pages/login/index',
    complete() { redirecting = false },
    fail() { signedOut = false; showExpiredToast() },
  })
}

function showExpiredToast() {
  if (!pendingToast) return
  pendingToast = false
  wx.showToast({ title: EXPIRED_MESSAGE, icon: 'none', duration: 3000 })
}

module.exports = { clearSession, expireSession, showExpiredToast }
