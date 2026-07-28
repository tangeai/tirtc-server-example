// thing-connect/weixin-mini-program/pages/devices/index.js
const { userApi, voipApi } = require('../../utils/api')
const {
  buildVideoUIConfig,
  normalizeAspectRatio,
  normalizeBoolean,
  normalizeCameraRotation,
  normalizeObjectFit,
  updateDeviceVideoProfileCache,
} = require('../../utils/voip-video-profile')
const {
  CALL_DIRECTION,
  buildVoipUIConfig,
  phoneDisplaySnapshot,
} = require('../../utils/voip-ui-config')
const { refreshVoipAuthState } = require('../../utils/voip-auth-refresh')
const app = getApp()

const SWIPE_THRESHOLD = 60
const SWIPE_OPEN_X = -88
const MAX_DEVICE_NAME_CHARS = 13
const AUTH_NAME_POOL = ['爸爸', '妈妈', '爷爷', '奶奶', '哥哥', '姐姐', '朋友']

function randomAuthNames(count = 4) {
  const names = AUTH_NAME_POOL.slice()
  for (let i = names.length - 1; i > 0; i -= 1) {
    const j = Math.floor(Math.random() * (i + 1))
    const current = names[i]
    names[i] = names[j]
    names[j] = current
  }
  return names.slice(0, count)
}

function normalizeMediaCodec(value) {
  return String(value || '').trim().toUpperCase()
}

function normalizeVideoCodec(value) {
  const codec = normalizeMediaCodec(value)
  return codec === 'NONE' ? '' : codec
}

function applyVideoUIConfig(wmpfVoip, device) {
  if (!wmpfVoip || typeof wmpfVoip.setUIConfig !== 'function') return
  const ui = buildVideoUIConfig(device)
  const phoneDisplay = phoneDisplaySnapshot()
  const config = buildVoipUIConfig(
    CALL_DIRECTION.MINI_PROGRAM_TO_DEVICE,
    ui,
    phoneDisplay.recommendedContainAspectRatio,
  )
  try {
    wmpfVoip.setUIConfig(config)
    console.info('[voip] outgoing video UI', {
      direction: CALL_DIRECTION.MINI_PROGRAM_TO_DEVICE,
      deviceRole: 'listener',
      miniProgramRole: 'caller',
      phoneDisplay,
      callerUI: config.callerUI,
      listenerUI: config.listenerUI,
      deviceProfile: ui,
    })
  } catch (error) {
    console.warn('设置 VoIP 视频 UI 失败，将使用微信插件默认值', error)
  }
}

function formatAudioCodecName(value, rate) {
  const codec = normalizeMediaCodec(value)
  if (!codec) return ''
  if (codec === 'ALAW') return 'G.711A'
  if (codec === 'AMR') {
    if (rate === 8000) return 'AMR-NB'
    if (rate === 16000) return 'AMR-WB'
    return 'AMR'
  }
  if (codec === 'OPUS') return 'Opus'
  return codec
}

function parseBindTimestamp(value) {
  if (!value) return 0
  const text = String(value).trim()
  const match = text.match(/^(\d{4})-(\d{2})-(\d{2})[T\s](\d{2}):(\d{2}):(\d{2})/)
  if (match) {
    const year = Number(match[1])
    const month = Number(match[2])
    const day = Number(match[3])
    const hour = Number(match[4])
    const minute = Number(match[5])
    const second = Number(match[6])
    return new Date(year, month - 1, day, hour, minute, second).getTime()
  }
  const fallback = Date.parse(text.replace(/-/g, '/').replace('T', ' '))
  return Number.isNaN(fallback) ? 0 : fallback
}

function formatAudioSummary(codec, rate) {
  if (!codec && !rate) return ''
  const rateText = rate ? `${rate / 1000}k` : ''
  return ['音频', formatAudioCodecName(codec, rate), rateText].filter(Boolean).join(' ')
}

Page({
  data: {
    deviceList: [],
    loading: false,
    initialized: false,
    authNameModalVisible: false,
    authName: '',
    authNameSuggestions: [],
    authDeviceId: '',
    authNameMode: 'authorize',
    contactRemark: '',
    profileOpenId: '',
    deviceNameModalVisible: false,
    editingDeviceId: '',
    editingDeviceName: '',
    continueAuthorizeAfterName: false,
  },

  _decorateDevice(device, previous = {}) {
    const upVideoMT = normalizeVideoCodec(device.up_video_mt)
    const downVideoMT = normalizeVideoCodec(device.down_video_mt)
    const downAudioMT = normalizeMediaCodec(device.down_audio_mt)
    const audioRate = Number(device.audio_rate) || 0
    const cameraRotation = normalizeCameraRotation(device.camera_rotation)
    const aspectRatio = normalizeAspectRatio(device.aspect_ratio)
    const horMirror = normalizeBoolean(device.hor_mirror)
    const vertMirror = normalizeBoolean(device.vert_mirror)
    const objectFit = normalizeObjectFit(device.object_fit)
    const hasCamera = !!upVideoMT
    const hasScreen = !!downVideoMT
    const voipRoomType = (hasCamera || hasScreen) ? 'video' : 'voice'
    return {
      ...device,
      voipAuthed: previous.voipAuthed || false,
      voipAuthState: previous.voipAuthState || 'unknown',
      authorizedDeviceName: previous.authorizedDeviceName || '',
      serverVoipAuthed: previous.serverVoipAuthed || false,
      deviceNamePending: false,
      slideX: 0,
      up_video_mt: upVideoMT,
      down_video_mt: downVideoMT,
      down_audio_mt: downAudioMT,
      audio_rate: audioRate,
      camera_rotation: cameraRotation,
      aspect_ratio: aspectRatio,
      hor_mirror: horMirror,
      vert_mirror: vertMirror,
      object_fit: objectFit,
      hasCamera,
      hasScreen,
      voipRoomType,
      callButtonText: voipRoomType === 'video' ? '视频呼叫' : '语音呼叫',
      mediaSummary: [
        hasCamera ? `摄像头 ${upVideoMT}` : '',
        hasScreen ? `屏显 ${downVideoMT}` : '',
        formatAudioSummary(downAudioMT, audioRate),
      ].filter(Boolean).join(' / '),
    }
  },

  onShow() {
    this.loadDevices()
  },

  onPullDownRefresh() {
    this.loadDevices({ stopPullDownRefresh: true })
  },

  async loadDevices(options = {}) {
    const { stopPullDownRefresh = false } = options
    if (this._loadPromise) {
      if (stopPullDownRefresh) {
        this._loadPromise.finally(() => wx.stopPullDownRefresh())
      }
      return this._loadPromise
    }

    this.setData({ loading: true })
    wx.showNavigationBarLoading()

    this._loadPromise = (async () => {
      try {
        this._closeOtherSwipes()
        const res = await userApi('/v1/user/device/list', 'GET')
        if (res.code === 200) {
          const existingMap = {}
          this.data.deviceList.forEach(d => { existingMap[d.device_id] = d })
          const list = (res.data || [])
            .slice()
            .sort((a, b) => parseBindTimestamp(b.bind_time) - parseBindTimestamp(a.bind_time))
            .map(d => this._decorateDevice(d, existingMap[d.device_id] || {}))
          // 每次列表刷新成功都以服务端最新结果整体更新缓存，供设备入呼同步读取。
          updateDeviceVideoProfileCache(list)
          this.setData({ deviceList: list, initialized: true })
          await refreshVoipAuthState({
            refreshOpenId: () => this._fetchOpenId(true),
            syncWechatAuthState: () => this._syncVoIPAuthState(),
            syncContactRemark: () => this._syncContactRemark(),
            syncServerAuthList: () => this._syncServerAuthList(),
            reconcileAuthorizedDevices: () => this._reconcileAuthorizedDevices(),
          })
        } else if (res.code === 401) {
          wx.removeStorageSync('token')
          wx.showToast({ title: '登录已过期，请重新登录', icon: 'none' })
          setTimeout(() => wx.redirectTo({ url: '/pages/login/index' }), 1000)
        }
      } catch (_) {
        wx.showToast({ title: '加载失败', icon: 'none' })
      } finally {
        this._loadPromise = null
        this.setData({ loading: false, initialized: true })
        wx.hideNavigationBarLoading()
        if (stopPullDownRefresh) {
          wx.stopPullDownRefresh()
        }
      }
    })()

    return this._loadPromise
  },

  _fetchOpenId(forceRefresh = false) {
    if (this._openIdPromise) return this._openIdPromise
    if (!forceRefresh && app.globalData.wxOpenId) return Promise.resolve(app.globalData.wxOpenId)
    this._openIdPromise = new Promise((resolve, reject) => {
      wx.login({
        success: (res) => {
          if (!res.code) {
            reject(new Error('微信登录未返回 code'))
            return
          }
          voipApi('/v1/voip/user/wechat-mini-login', 'POST', {
            code: res.code,
            wx_app_id: app.globalData.wxAppId,
          }).then(r => {
            const openid = r && r.code === 0 && r.data && r.data.wx_user_openid
            if (!openid) {
              reject(new Error((r && r.msg) || '获取微信 OpenID 失败'))
              return
            }
            app.globalData.wxOpenId = openid
            resolve(openid)
          }).catch(reject)
        },
        fail: reject,
      })
    })
    const pending = this._openIdPromise
    pending.then(
      () => { if (this._openIdPromise === pending) this._openIdPromise = null },
      () => { if (this._openIdPromise === pending) this._openIdPromise = null },
    )
    return pending
  },

  _syncVoIPAuthState() {
    return new Promise((resolve) => {
      wx.getDeviceVoIPList({
        success: (res) => {
          const states = {}
          ;(res.list || []).forEach((item) => {
            states[item.sn] = item.status === 1 ? 'authorized' : 'disabled'
          })
          const list = this.data.deviceList.map((device) => {
            const voipAuthState = states[device.device_id] || 'missing'
            return {
              ...device,
              voipAuthState,
              voipAuthed: voipAuthState === 'authorized',
            }
          })
          this.setData({ deviceList: list })
          resolve()
        },
        fail: () => {
          const list = this.data.deviceList.map(device => ({
            ...device,
            voipAuthState: 'unknown',
            voipAuthed: false,
          }))
          this.setData({ deviceList: list })
          resolve()
        },
      })
    })
  },

  async _syncServerAuthList() {
    try {
      await this._fetchOpenId()
      const res = await voipApi('/v1/voip/user/auth-list', 'GET', {
        wx_app_id: app.globalData.wxAppId,
      })
      if (!res || res.code !== 0 || !res.data) return false
      const snapshots = {}
      const serverAuthedDevices = new Set()
      ;(res.data.list || []).forEach((item) => {
        snapshots[item.device_id] = item.authorized_device_name || ''
        serverAuthedDevices.add(item.device_id)
      })
      const list = this.data.deviceList.map((device) => {
        const authorizedDeviceName = snapshots[device.device_id] || device.authorizedDeviceName || ''
        return {
          ...device,
          serverVoipAuthed: serverAuthedDevices.has(device.device_id),
          authorizedDeviceName,
          deviceNamePending: !!(
            device.voipAuthed
            && authorizedDeviceName
            && device.device_name
            && authorizedDeviceName !== device.device_name
          ),
        }
      })
      this.setData({ deviceList: list })
      return true
    } catch (_) {
      // 微信侧授权状态仍然可用，名称快照稍后刷新。
      return false
    }
  },

  async _reconcileAuthorizedDevices() {
    let serverAuthChanged = false
    const missingOnServer = this.data.deviceList.filter(device => (
      device.voipAuthState === 'authorized' && !device.serverVoipAuthed
    ))
    for (const device of missingOnServer) {
      try {
        await this._reportVoIPAuth(
          device.device_id,
          this.data.contactRemark,
          device.authorizedDeviceName || device.device_name || device.device_id,
          false,
          false,
        )
        serverAuthChanged = true
      } catch (_) {
        // 单台设备同步失败不阻断列表，其授权状态会在下次进入页面时重试。
      }
    }

    // getDeviceVoIPList 成功且明确为 missing/disabled 时，微信侧授权已经
    // 不可用。主动清理服务端快照，避免设备仍显示并拨打这个联系人。
    // unknown 代表本次状态读取失败，不能据此删除。
    const invalidOnWechat = this.data.deviceList.filter(device => (
      device.serverVoipAuthed
      && (device.voipAuthState === 'missing' || device.voipAuthState === 'disabled')
    ))
    for (const device of invalidOnWechat) {
      try {
        const wxOpenId = await this._fetchOpenId()
        const res = await voipApi('/v1/voip/user/delete-auth', 'POST', {
          device_id: device.device_id,
          wx_app_id: app.globalData.wxAppId,
          wx_open_id: wxOpenId,
        })
        if (!res || res.code !== 0) {
          throw new Error((res && res.msg) || '同步授权失效状态失败')
        }
        serverAuthChanged = true
      } catch (_) {
        // 下次进入或下拉时重试；不把网络错误误报为微信授权错误。
      }
    }
    return serverAuthChanged
  },

  async _syncContactRemark() {
    try {
      // loadDevices 已统一刷新本轮微信登录关系；这里只复用当前 OpenID，
      // 避免与 auth-list 各自再次调用 wechat-mini-login。
      await this._fetchOpenId()
      const res = await voipApi('/v1/voip/user/contact-remark', 'GET', {
        wx_app_id: app.globalData.wxAppId,
      })
      if (!res || res.code !== 0 || !res.data) return
      const contactRemark = res.data.remark || ''
      this.setData({ contactRemark })
    } catch (_) {
      // 保留当前页面已有名称，用户仍可从右上角入口重新保存。
    }
  },

  onCopyOpenId() {
    const openid = app.globalData.wxOpenId
    if (!openid) { wx.showToast({ title: '暂未获取到 OpenID', icon: 'none' }); return }
    wx.showModal({
      title: '微信 OpenID',
      content: openid,
      cancelText: '关闭',
      confirmText: '复制',
      confirmColor: '#07c160',
      success: (res) => {
        if (res.confirm) {
          wx.setClipboardData({ data: openid, success: () => wx.showToast({ title: '已复制', icon: 'success' }) })
        }
      }
    })
  },

  onCopyProfileOpenId() {
    const openid = this.data.profileOpenId || app.globalData.wxOpenId
    if (!openid) {
      wx.showToast({ title: '暂未获取到 OpenID', icon: 'none' })
      return
    }
    wx.setClipboardData({
      data: openid,
      success: () => wx.showToast({ title: '已复制', icon: 'success' }),
    })
  },

  goAdd() {
    wx.navigateTo({ url: '/pages/bind/index' })
  },

  noop() {},

  _openDeviceNameModal(deviceId, continueAuthorize = false) {
    const device = this.data.deviceList.find(d => d.device_id === deviceId)
    this.setData({
      deviceNameModalVisible: true,
      editingDeviceId: deviceId,
      editingDeviceName: (device && device.device_name) || '',
      continueAuthorizeAfterName: continueAuthorize,
    })
  },

  onEditDeviceName(e) {
    this._openDeviceNameModal(e.currentTarget.dataset.id, false)
  },

  onDeviceNameInput(e) {
    this.setData({ editingDeviceName: e.detail.value })
  },

  onCancelDeviceName() {
    this.setData({
      deviceNameModalVisible: false,
      editingDeviceId: '',
      editingDeviceName: '',
      continueAuthorizeAfterName: false,
    })
  },

  async onConfirmDeviceName() {
    const deviceId = this.data.editingDeviceId
    const deviceName = String(this.data.editingDeviceName || '').trim()
    if (!deviceName) {
      wx.showToast({ title: '请输入设备名称', icon: 'none' })
      return
    }
    if (Array.from(deviceName).length > MAX_DEVICE_NAME_CHARS) {
      wx.showToast({ title: '设备名称最多 13 个字符', icon: 'none' })
      return
    }
    const continueAuthorize = this.data.continueAuthorizeAfterName
    wx.showLoading({ title: '保存中...', mask: true })
    try {
      const res = await userApi('/v1/user/device/name', 'PUT', {
        device_id: deviceId,
        device_name: deviceName,
      })
      if (!res || res.code !== 200) throw new Error((res && res.msg) || '保存失败')
      const list = this.data.deviceList.map((device) => {
        if (device.device_id !== deviceId) return device
        return {
          ...device,
          device_name: deviceName,
          deviceNamePending: !!(
            device.voipAuthed
            && device.authorizedDeviceName
            && device.authorizedDeviceName !== deviceName
          ),
        }
      })
      this.onCancelDeviceName()
      this.setData({ deviceList: list })
      if (continueAuthorize) {
        await this._continueAuthorize(deviceId)
      } else {
        const device = list.find(d => d.device_id === deviceId)
        if (device && device.deviceNamePending) {
          this._showDeviceNamePendingHelp()
        } else {
          wx.showToast({ title: '设备名称已保存' })
        }
      }
    } catch (err) {
      wx.showModal({
        title: '保存失败',
        content: err.message || JSON.stringify(err),
        showCancel: false,
      })
    } finally {
      wx.hideLoading()
    }
  },

  _showDeviceNamePendingHelp() {
    wx.showModal({
      title: '名称已保存',
      content: '微信来电名称仍使用上次授权时的旧名称。需要在微信“最近使用”中删除本小程序以清空授权记录，再重新进入小程序完成授权，新名称才会生效。',
      showCancel: false,
    })
  },

  onDeviceNamePendingHelp() {
    this._showDeviceNamePendingHelp()
  },

  logout() {
    wx.removeStorageSync('token')
    app.globalData.wxOpenId = ''
    this._openIdPromise = null
    wx.redirectTo({ url: '/pages/login/index' })
  },

  onSwipeStart(e) {
    const { id, index } = e.currentTarget.dataset
    const touch = e.touches[0]
    this._swipeTouch = {
      deviceId: id,
      index,
      startX: touch.clientX,
      startY: touch.clientY,
    }
    this._closeOtherSwipes(id)
  },

  onSwipeEnd(e) {
    if (!this._swipeTouch) return
    const touch = e.changedTouches[0]
    const { deviceId, index, startX, startY } = this._swipeTouch
    this._swipeTouch = null
    if (deviceId !== e.currentTarget.dataset.id) return

    const dx = touch.clientX - startX
    const dy = touch.clientY - startY
    if (Math.abs(dy) > Math.abs(dx)) return

    let targetX = this.data.deviceList[index] && this.data.deviceList[index].slideX ? this.data.deviceList[index].slideX : 0
    if (dx <= -SWIPE_THRESHOLD) {
      targetX = SWIPE_OPEN_X
    } else if (dx >= SWIPE_THRESHOLD) {
      targetX = 0
    } else if (this.data.deviceList[index] && this.data.deviceList[index].slideX) {
      targetX = SWIPE_OPEN_X
    }
    this.setData({ [`deviceList[${index}].slideX`]: targetX })
  },

  _closeOtherSwipes(activeDeviceId = '') {
    const updates = {}
    this.data.deviceList.forEach((item, index) => {
      if (item.device_id !== activeDeviceId && item.slideX) {
        updates[`deviceList[${index}].slideX`] = 0
      }
    })
    if (Object.keys(updates).length > 0) {
      this.setData(updates)
    }
  },

  // ── 解绑 ──────────────────────────────────────────
  onUnbind(e) {
    const deviceId = e.currentTarget.dataset.id
    const deviceIndex = this.data.deviceList.findIndex(d => d.device_id === deviceId)
    wx.showModal({
      title: '解绑设备',
      content: `确认解绑设备 ${deviceId}？`,
      confirmColor: '#e64340',
      success: async (res) => {
        if (!res.confirm) {
          if (deviceIndex >= 0) {
            this.setData({ [`deviceList[${deviceIndex}].slideX`]: 0 })
          }
          return
        }
        try {
          const res = await userApi('/v1/user/device/reset', 'DELETE', { device_id: deviceId })
          if (res.code !== 200) throw new Error(res.msg || '解绑失败')
          // user-server 的解绑清理会同步删除 VoIP 授权，无需在解绑后
          // 再调用一个已经失去设备所有权的 delete-auth。
          wx.showToast({ title: '已解绑' })
          this.loadDevices()
        } catch (err) {
          wx.showToast({ title: err.msg || '解绑失败', icon: 'none' })
        }
      }
    })
  },

  onPageTap() {
    this._closeOtherSwipes()
  },

  // ── VoIP 授权 ─────────────────────────────────────
  _openAuthNameModal(deviceId, mode, currentName = '') {
    const suggestions = randomAuthNames()
    this.setData({
      authNameModalVisible: true,
      authName: currentName || suggestions[0],
      authNameSuggestions: suggestions,
      authDeviceId: deviceId,
      authNameMode: mode,
    })
  },

  onAuthorize(e) {
    const deviceId = e.currentTarget.dataset.id
    const device = this.data.deviceList.find(d => d.device_id === deviceId)
    if (device && device.voipAuthState === 'disabled') {
      wx.showModal({
        title: '授权已关闭',
        content: '微信不会再次弹出授权框。请点击右上角“…”进入设置，在“语音、视频通话提醒”中重新开启该设备。',
        showCancel: false,
      })
      return
    }
    if (device && device.voipAuthState === 'unknown') {
      wx.showModal({
        title: '授权状态未知',
        content: '暂时无法读取微信通话授权状态，请检查网络后下拉刷新。为避免误触发授权，本次不会调用授权接口。',
        showCancel: false,
      })
      return
    }
    if (!device || !String(device.device_name || '').trim()) {
      this._openDeviceNameModal(deviceId, true)
      return
    }
    this._continueAuthorize(deviceId)
  },

  _continueAuthorize(deviceId) {
    if (this.data.contactRemark) {
      return this._authorizeDevice(deviceId, this.data.contactRemark)
    }
    this._openAuthNameModal(deviceId, 'authorize')
    return Promise.resolve()
  },

  onEditContactRemark() {
    this._openAuthNameModal('', 'profile', this.data.contactRemark)
    const cachedOpenId = app.globalData.wxOpenId || ''
    this.setData({ profileOpenId: cachedOpenId })
    if (cachedOpenId) return

    this._fetchOpenId().then(openid => {
      if (this.data.authNameModalVisible && this.data.authNameMode === 'profile') {
        this.setData({ profileOpenId: openid })
      }
    }).catch(() => {
      wx.showToast({ title: 'OpenID 获取失败，请稍后重试', icon: 'none' })
    })
  },

  onAuthNameInput(e) {
    this.setData({ authName: e.detail.value })
  },

  onSelectAuthName(e) {
    this.setData({ authName: e.currentTarget.dataset.name })
  },

  onCancelAuthName() {
    this.setData({
      authNameModalVisible: false,
      authName: '',
      authNameSuggestions: [],
      authDeviceId: '',
      authNameMode: 'authorize',
    })
  },

  onConfirmAuthName() {
    const deviceId = this.data.authDeviceId
    const remark = String(this.data.authName || '').trim()
    if (!remark) {
      wx.showToast({ title: '请输入联系人名称', icon: 'none' })
      return
    }
    if (Array.from(remark).length > 64) {
      wx.showToast({ title: '名称最多 64 个字符', icon: 'none' })
      return
    }
    const mode = this.data.authNameMode
    this.onCancelAuthName()
    if (mode === 'profile') {
      this._saveGlobalContactRemark(remark)
    } else {
      this._authorizeDevice(deviceId, remark)
    }
  },

  async _reportVoIPAuth(
    deviceId,
    remark,
    deviceName,
    authorizationCreated,
    refreshLogin = true,
  ) {
    // 每次上报前刷新微信登录关系，服务端据此校验 wx_open_id，
    // 同时覆盖 Redis 重启或 24h 登录绑定过期的恢复场景。
    // 页面自动对账已经在本轮统一刷新，可显式复用，避免重复登录。
    const wxOpenId = await this._fetchOpenId(refreshLogin)
    const res = await voipApi('/v1/voip/user/report-auth', 'POST', {
      device_id: deviceId,
      wx_app_id: app.globalData.wxAppId,
      wx_model_id: app.globalData.modelId,
      wx_open_id: wxOpenId,
      remark,
      device_name: deviceName,
      authorization_created: !!authorizationCreated,
    })
    if (!res || res.code !== 0) {
      throw new Error((res && res.msg) || '保存联系人名称失败')
    }
  },

  async _authorizeDevice(deviceId, remark) {
    const device = this.data.deviceList.find(d => d.device_id === deviceId)
    const deviceName = String((device && device.device_name) || '').trim()
    if (!deviceName) {
      this._openDeviceNameModal(deviceId, true)
      return
    }
    wx.showLoading({ title: '授权中...', mask: true })
    let wechatAuthorized = false
    try {
      const ticketRes = await voipApi('/v1/voip/user/sn-ticket', 'POST', {
        device_id: deviceId,
        wx_app_id: app.globalData.wxAppId,
      })
      if (!ticketRes || ticketRes.code !== 0 || !ticketRes.data || !ticketRes.data.sn_ticket) {
        throw new Error((ticketRes && ticketRes.msg) || '获取票据失败')
      }
      // sn-ticket 同时返回数据库中的当前设备名。授权必须使用这个最终值，
      // 避免列表加载后名称被其他入口修改，导致微信侧名称与服务端快照不一致。
      const authorizationDeviceName = String(ticketRes.data.device_name || '').trim()
      if (!authorizationDeviceName) {
        throw new Error('请先设置设备名称')
      }
      let alreadyAuthorized = false
      try {
        await new Promise((resolve, reject) => {
          wx.requestDeviceVoIP({
            sn: deviceId,
            snTicket: ticketRes.data.sn_ticket,
            modelId: app.globalData.modelId,
            deviceName: authorizationDeviceName,
            success: resolve,
            fail: reject,
          })
        })
        wechatAuthorized = true
      } catch (err) {
        if (!err || err.errCode !== 10001) throw err
        alreadyAuthorized = true
        wechatAuthorized = true
      }
      await this._reportVoIPAuth(
        deviceId,
        remark,
        authorizationDeviceName,
        !alreadyAuthorized,
      )
      const list = this.data.deviceList.map((item) => {
        if (item.device_id !== deviceId) return item
        const authorizedDeviceName = alreadyAuthorized
          ? (item.authorizedDeviceName || deviceId)
          : authorizationDeviceName
        return {
          ...item,
          device_name: authorizationDeviceName,
          voipAuthed: true,
          voipAuthState: 'authorized',
          serverVoipAuthed: true,
          authorizedDeviceName,
          deviceNamePending: authorizedDeviceName !== authorizationDeviceName,
        }
      })
      this.setData({ deviceList: list, contactRemark: remark })
      wx.showToast({ title: alreadyAuthorized ? '名称已保存' : '授权成功' })
    } catch (err) {
      if (wechatAuthorized) {
        const list = this.data.deviceList.map(d => (
          d.device_id === deviceId ? { ...d, voipAuthed: true } : d
        ))
        this.setData({ deviceList: list })
        wx.showModal({
          title: '名称保存失败',
          content: `${err.message || JSON.stringify(err)}。微信授权已完成，请点击右上角用户图标设置“我的联系人名称”后重试。`,
          showCancel: false,
        })
      } else {
        wx.showModal({ title: '授权失败', content: err.message || JSON.stringify(err), showCancel: false })
      }
    } finally {
      wx.hideLoading()
    }
  },

  async _saveGlobalContactRemark(remark) {
    wx.showLoading({ title: '保存中...', mask: true })
    try {
      await this._fetchOpenId(true)
      const res = await voipApi('/v1/voip/user/contact-remark', 'PUT', {
        wx_app_id: app.globalData.wxAppId,
        remark,
      })
      if (!res || res.code !== 0) {
        throw new Error((res && res.msg) || '保存联系人名称失败')
      }
      this.setData({ contactRemark: remark })
      wx.showToast({ title: '联系人名称已保存' })
    } catch (err) {
      wx.showModal({ title: '保存失败', content: err.message || JSON.stringify(err), showCancel: false })
    } finally {
      wx.hideLoading()
    }
  },

  // ── 呼叫 ──────────────────────────────────────────
  async onCall(e) {
    const deviceId = e.currentTarget.dataset.id
    const device = this.data.deviceList.find(d => d.device_id === deviceId)
    const wmpfVoip = requirePlugin('wmpf-voip').default
    wx.showLoading({ title: '呼叫中...', mask: true })
    try {
      if (!device || !String(device.device_name || '').trim()) {
        throw new Error('请先设置设备名称')
      }
      if (device.online === false) {
        throw new Error('设备当前离线，请等待设备上线后再呼叫')
      }
      if (device.voipAuthState === 'disabled') {
        throw new Error('该设备的微信通话授权已关闭，请先在小程序设置中重新开启')
      }
      if (device.voipAuthState === 'missing') {
        throw new Error('该设备尚未授权，请先完成授权')
      }
      if (!wmpfVoip || typeof wmpfVoip.callDevice !== 'function') {
        throw new Error('当前 VoIP 插件不支持 callDevice，请确认插件版本不低于 2.4.0')
      }
      if (!wmpfVoip.CALL_PAGE_PATH) {
        throw new Error('VoIP 插件未提供通话页面地址，请重新构建小程序')
      }
      const enableCallerCamera = !!(device && device.hasScreen)
      const enableListenerCamera = !!(device && device.hasCamera)
      const roomType = (enableCallerCamera || enableListenerCamera) ? 'video' : 'voice'
      if (roomType === 'video') {
        applyVideoUIConfig(wmpfVoip, device)
      }
      const displayDeviceName = device.authorizedDeviceName || device.device_name
      const { roomId } = await wmpfVoip.callDevice({
        sn: deviceId,
        modelId: app.globalData.modelId,
        roomType,
        enableCallerCamera,
        enableListenerCamera,
        nickName: this.data.contactRemark || '微信用户',
        deviceName: displayDeviceName,
        isCloud: true,
      })
      if (!roomId) throw new Error('创建房间失败')
      app.globalData.currentCall = { deviceId, roomId }
      wx.hideLoading()
      wx.redirectTo({ url: wmpfVoip.CALL_PAGE_PATH })
    } catch (err) {
      wx.showModal({ title: '呼叫失败', content: err.message || JSON.stringify(err), showCancel: false })
    } finally {
      wx.hideLoading()
    }
  },
})
