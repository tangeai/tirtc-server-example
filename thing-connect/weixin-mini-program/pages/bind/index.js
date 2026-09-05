const { expireSession } = require('../../utils/session')
// thing-connect/weixin-mini-program/pages/bind/index.js
const { userApi } = require('../../utils/api')

function normalizeDeviceId(value) {
  return String(value || '').trim().toUpperCase()
}

function extractDeviceIdFromScan(scanResult) {
  const raw = String(scanResult || '').trim()
  if (!raw) return ''
  const decoded = (() => {
    try {
      return decodeURIComponent(raw)
    } catch (_) {
      return raw
    }
  })()
  const direct = normalizeDeviceId(decoded)
  if (!/[?=&]/.test(decoded)) return direct

  const queryPart = decoded.includes('?') ? decoded.split('?')[1] : decoded
  const pairs = queryPart.split(/[&#]/)
  for (const pair of pairs) {
    const [key, value = ''] = pair.split('=')
    const normalizedKey = String(key || '').trim().toLowerCase()
    if (['device_id', 'deviceid', 'sn', 'id'].includes(normalizedKey)) {
      const deviceId = normalizeDeviceId(value)
      if (deviceId) return deviceId
    }
  }
  return direct
}

Page({
  data: {
    activeTab: 'code',
    bindCode: '',
    bindDeviceId: '',
    bindLoading: false,
    errMsg: '',
  },

  switchTab(e) {
    this.setData({ activeTab: e.currentTarget.dataset.tab, errMsg: '' })
  },

  onBindCodeInput(e) {
    this.setData({ bindCode: e.detail.value.replace(/\D/g, '').slice(0, 6) })
  },

  onBindDeviceIdInput(e) {
    this.setData({ bindDeviceId: normalizeDeviceId(e.detail.value) })
  },

  scanDeviceId() {
    wx.scanCode({
      scanType: ['qrCode'],
      success: (res) => {
        const deviceId = extractDeviceIdFromScan(res.result || res.path || '')
        if (!deviceId) {
          wx.showToast({ title: '未识别到设备 ID', icon: 'none' })
          return
        }
        this.setData({ bindDeviceId: deviceId, errMsg: '' })
      },
      fail: (err) => {
        if (err && String(err.errMsg || '').includes('cancel')) return
        wx.showToast({ title: '扫码失败', icon: 'none' })
      },
    })
  },

  async doBindByCode() {
    const { bindCode } = this.data
    if (bindCode.length !== 6) { this.setData({ errMsg: '请输入6位数字验证码' }); return }
    this.setData({ bindLoading: true, errMsg: '' })
    try {
      const res = await userApi('/v1/user/device/bind', 'POST', { code: bindCode })
      if (res.code === 200) {
        wx.showToast({ title: '绑定成功' })
        setTimeout(() => wx.navigateBack(), 1000)
      } else if (res.code === 401) {
        this._goRelogin()
      } else {
        this.setData({ errMsg: res.msg || '绑定失败' })
      }
    } catch (e) {
      if (e.sessionExpired) return
      this.setData({ errMsg: e.msg || '绑定失败' })
    } finally {
      this.setData({ bindLoading: false })
    }
  },

  async doBindById() {
    const bindDeviceId = normalizeDeviceId(this.data.bindDeviceId)
    if (!bindDeviceId) { this.setData({ errMsg: '请输入设备 ID' }); return }
    this.setData({ bindDeviceId })
    this.setData({ bindLoading: true, errMsg: '' })
    try {
      const res = await userApi('/v1/user/device/bind-by-id', 'POST', { device_id: bindDeviceId })
      if (res.code === 200) {
        wx.showToast({ title: '绑定成功' })
        setTimeout(() => wx.navigateBack(), 1000)
      } else if (res.code === 401) {
        this._goRelogin()
      } else {
        this.setData({ errMsg: res.msg || '绑定失败' })
      }
    } catch (e) {
      if (e.sessionExpired) return
      this.setData({ errMsg: e.msg || '绑定失败' })
    } finally {
      this.setData({ bindLoading: false })
    }
  },

  _goRelogin() {
    expireSession()
  },
})
