const { expireSession } = require('../../utils/session')
const { buildDeviceWebPage } = require('../../utils/device-web-page')
const { userApi } = require('../../utils/api')
const app = getApp()

Page({
  data: { src: '', error: '', loading: true },

  onLoad(options) {
    wx.hideShareMenu({ menus: ['shareAppMessage', 'shareTimeline'] })
    try {
      this._options = { kind: options.kind, deviceId: decodeURIComponent(options.device_id || '') }
    } catch {
      this.setData({ loading: false, error: '设备入口无效，请返回设备列表重新打开' })
      return
    }
    this.openPage()
  },

  async openPage() {
    if (this._opening) return
    if (!this._options) return this.goBack()
    this._opening = true
    this.setData({ src: '', error: '', loading: true })
    try {
      const token = wx.getStorageSync('token')
      if (!token) return this.login()
      const result = await userApi('/v1/user/device/list')
      if (this._disposed) return
      if (result.code !== 200) throw result
      const device = (result.data || []).find(d => d.device_id === this._options.deviceId)
      if (!device) throw new Error('设备已解绑或不可用，请返回设备列表')
      if (this._options.kind === 'live' && device.online !== true) {
        throw new Error('设备当前离线，请检查设备电源和网络后重试')
      }
      const page = buildDeviceWebPage(app.globalData.userServerBaseUrl,
        this._options.kind, device.device_id, token)
      wx.setNavigationBarTitle({ title: page.title })
      wx.showNavigationBarLoading()
      this.setData({ src: page.url })
      this._timer = setTimeout(() => this.onWebError(), 20000)
    } catch (error) {
      if (this._disposed) return
      if (error.sessionExpired) return
      if (error.code === 401) return this.login()
      this.setData({ loading: false, error: error.message || error.msg || '暂时无法打开，请检查网络后重试' })
    } finally {
      this._opening = false
    }
  },

  login() {
    expireSession()
  },

  onWebLoad() {
    clearTimeout(this._timer)
    wx.hideNavigationBarLoading()
    this.setData({ loading: false })
  },

  onWebError() {
    if (this._disposed) return
    clearTimeout(this._timer)
    wx.hideNavigationBarLoading()
    // Do not log web-view events: their URL can contain the bootstrap credential.
    this.setData({ src: '', loading: false, error: '页面暂时无法打开，请检查网络后重试。如持续失败，请联系管理员。' })
  },

  goBack() {
    wx.navigateBack({ fail: () => wx.reLaunch({ url: '/pages/devices/index' }) })
  },

  onUnload() {
    this._disposed = true
    clearTimeout(this._timer)
    wx.hideNavigationBarLoading()
    this.data.src = ''
  },
})
