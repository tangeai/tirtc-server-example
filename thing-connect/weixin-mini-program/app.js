const {
  buildVideoUIConfig,
  cachedProfile,
  incomingDeviceID,
} = require('./utils/voip-video-profile')
const {
  CALL_DIRECTION,
  buildVoipUIConfig,
  phoneDisplaySnapshot,
} = require('./utils/voip-ui-config')
const { parseIncomingQuery } = require('./utils/voip-incoming-query')

const VOIP_TRACE_VERSION = '2026-07-24-directional-ui-v10'
let voipTraceSequence = 0

function traceValue(value) {
  try {
    const json = JSON.stringify(value, (key, item) => (
      /ticket|token|signature|nonce|openid|listenerid/i.test(key)
        ? '[已隐藏]'
        : item
    ))
    return json === undefined ? String(value) : json
  } catch (error) {
    return '[无法序列化: ' + String(error) + ']'
  }
}

function voipTrace(label, value) {
  voipTraceSequence += 1
  console.log(
    '[voip-trace #' + voipTraceSequence + '] ' + label + ' = ' + traceValue(value),
  )
}

App({
  onLaunch() {
    voipTrace('build.version', VOIP_TRACE_VERSION)
    this._incomingVideoUIConfigTimer = null
    this.globalData.wxAppId = wx.getAccountInfoSync().miniProgram.appId
    const wmpfVoip = requirePlugin('wmpf-voip').default
    this._wmpfVoip = wmpfVoip
    this._applyIncomingVideoUIConfig(
      wmpfVoip.getPluginEnterOptions && wmpfVoip.getPluginEnterOptions(),
      'plugin-enter',
    )
    wmpfVoip.onVoipEvent((event) => {
      const eventName = event && event.eventName
      voipTrace('plugin.event', eventName)
      if (eventName === 'cancelVoip') {
        this._clearIncomingVideoUIConfigTimer()
        this._finishCurrentCall(event && event.roomId, true, eventName)
      } else if (eventName === 'abortVoip'
        || eventName === 'hangUpVoip'
        || eventName === 'joinFailCaller') {
        this._clearIncomingVideoUIConfigTimer()
        // 0x2001 通常也会结束设备会话；同时补一条业务通知，覆盖异常断网、
        // 加房失败等没有成功送达媒体层挂断信令的情况。
        this._finishCurrentCall(event && event.roomId, true, eventName)
      } else if (eventName === 'rejectVoip'
        || eventName === 'busyVoip'
        || eventName === 'endVoip'
        || eventName === 'finishVoip') {
        this._clearIncomingVideoUIConfigTimer()
        this._finishCurrentCall(event && event.roomId, false, eventName)
      } else if (eventName === 'callPageOnShow' && wmpfVoip.getPluginOnloadOptions) {
        // 前台 reLaunch 时 enter options 可能不是最新值，按官方建议补读 onLoad options。
        this._applyIncomingVideoUIConfig(wmpfVoip.getPluginOnloadOptions(), 'plugin-onload')
        // callPageOnShow 是从插件 onShow 的当前调用栈同步派发的。插件会在事件
        // 回调返回后继续 initByListener；用 0ms 定时器补一次，并合并连续事件，
        // 让补偿调用尽量落在建链前，同时避免重复排队和旧 options 覆盖新状态。
        this._scheduleIncomingVideoUIConfig(wmpfVoip)
      }
    })
  },

  onShow() {
    const wmpfVoip = this._wmpfVoip
    if (wmpfVoip && wmpfVoip.getPluginEnterOptions) {
      this._applyIncomingVideoUIConfig(wmpfVoip.getPluginEnterOptions(), 'app-show')
    }
  },

  _applyIncomingVideoUIConfig(options, source) {
    const wmpfVoip = this._wmpfVoip
    if (!wmpfVoip || typeof wmpfVoip.setUIConfig !== 'function') {
      voipTrace(source + '.setUIConfig.unavailable', typeof (wmpfVoip && wmpfVoip.setUIConfig))
      return
    }
    const query = parseIncomingQuery(options, source, voipTrace)
    const deviceID = incomingDeviceID(query)
    const ui = buildVideoUIConfig(query, cachedProfile(deviceID))
    voipTrace(source + '.ui.built', { deviceID, ui })
    if (Object.keys(ui).length) {
      this._setIncomingVideoUIConfig(ui, deviceID, source)
    } else {
      voipTrace(source + '.setUIConfig.skipped', 'UI 配置为空')
    }
  },

  _scheduleIncomingVideoUIConfig(wmpfVoip) {
    this._clearIncomingVideoUIConfigTimer()
    this._incomingVideoUIConfigTimer = setTimeout(() => {
      this._incomingVideoUIConfigTimer = null
      if (wmpfVoip !== this._wmpfVoip || typeof wmpfVoip.getPluginOnloadOptions !== 'function') return
      this._applyIncomingVideoUIConfig(
        wmpfVoip.getPluginOnloadOptions(),
        'plugin-onload-deferred',
      )
    }, 0)
  },

  _clearIncomingVideoUIConfigTimer() {
    if (this._incomingVideoUIConfigTimer === null) return
    clearTimeout(this._incomingVideoUIConfigTimer)
    this._incomingVideoUIConfigTimer = null
  },

  _setIncomingVideoUIConfig(ui, deviceID, source) {
    const wmpfVoip = this._wmpfVoip
    if (!wmpfVoip || typeof wmpfVoip.setUIConfig !== 'function' || !Object.keys(ui).length) return
    try {
      const phoneDisplay = phoneDisplaySnapshot()
      const screenAspectRatio = phoneDisplay.recommendedContainAspectRatio
      const useScreenAspectRatio = ui.objectFit === 'contain'
        && Number.isFinite(screenAspectRatio)
      const config = buildVoipUIConfig(
        CALL_DIRECTION.DEVICE_TO_MINI_PROGRAM,
        ui,
        screenAspectRatio,
      )
      const objectFitBeforeCall = {
        requested: ui.objectFit,
        callerUI: config.callerUI.objectFit,
        listenerUI: config.listenerUI.objectFit,
        callerAspectRatio: config.callerUI.aspectRatio,
        listenerAspectRatio: config.listenerUI.aspectRatio,
      }
      voipTrace(source + '.setUIConfig.contain.diagnostic', {
        deviceRole: 'caller',
        miniProgramRole: 'listener',
        phoneDisplay,
        profileAspectRatio: ui.aspectRatio,
        appliedAspectRatio: config.listenerUI.aspectRatio,
        screenAspectRatioApplied: useScreenAspectRatio,
        recommendedContainAspectRatio: phoneDisplay.recommendedContainAspectRatio,
        aspectRatioDifference: Number.isFinite(phoneDisplay.recommendedContainAspectRatio)
          && Number.isFinite(config.listenerUI.aspectRatio)
          ? config.listenerUI.aspectRatio - phoneDisplay.recommendedContainAspectRatio
          : null,
        activeUI: config.listenerUI,
      })
      voipTrace(source + '.setUIConfig.objectFit.before', objectFitBeforeCall)
      voipTrace(source + '.setUIConfig.call', config)
      const result = wmpfVoip.setUIConfig(config)
      voipTrace(source + '.setUIConfig.objectFit.after', {
        deviceID,
        result,
        callerUI: config.callerUI.objectFit,
        listenerUI: config.listenerUI.objectFit,
      })
    } catch (error) {
      voipTrace(source + '.setUIConfig.error', {
        name: error && error.name,
        message: error && error.message,
        stack: error && error.stack,
      })
      console.warn('设置 VoIP 视频 UI 失败，将使用微信插件默认值', error)
    }
  },

  _finishCurrentCall(roomId, notifyDevice, eventName) {
    const call = this.globalData.currentCall
    if (!call) return
    if (!roomId || roomId !== call.roomId) {
      voipTrace('plugin.event.ignored', {
        eventName,
        eventRoomId: roomId || '',
        currentRoomId: call.roomId,
      })
      return
    }
    const { deviceId } = call
    this.globalData.currentCall = null
    if (!notifyDevice) return
    const token = wx.getStorageSync('token') || ''
    wx.request({
      url: this.globalData.voipServerBaseUrl + '/v1/voip/user/cancel',
      method: 'POST',
      header: { 'content-type': 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}) },
      data: { device_id: deviceId, wx_room_id: roomId },
      fail: (error) => voipTrace('user.cancel.failed', { deviceId, roomId, error }),
    })
  },

  globalData: {
    // 替换为实际部署地址
    userServerBaseUrl: 'https://srv-open.tangeopen.com',
    voipServerBaseUrl: 'https://srv-open.tangeopen.com',
    // 替换为微信 IoT 平台分配的 ModelID
    modelId: 'HRHY_vJ9mHI2KQhd6yvj9Q',
    wxAppId: 'wx27d4b2d7eb37eb58',
    wxOpenId: '',
    currentCall: null,
  },
})
