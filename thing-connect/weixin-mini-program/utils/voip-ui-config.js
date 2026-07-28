const CALL_DIRECTION = {
  DEVICE_TO_MINI_PROGRAM: 'device-to-mini-program',
  MINI_PROGRAM_TO_DEVICE: 'mini-program-to-device',
}

function validAspectRatio(value) {
  const ratio = Number(value)
  return Number.isFinite(ratio) && ratio > 0 ? ratio : null
}

function phoneDisplaySnapshot(wxAPI) {
  try {
    const runtime = wxAPI || wx
    const info = typeof runtime.getWindowInfo === 'function'
      ? runtime.getWindowInfo()
      : runtime.getSystemInfoSync()
    const screenWidth = Number(info.screenWidth || info.windowWidth)
    const screenHeight = Number(info.screenHeight || info.windowHeight)
    return {
      screenWidth,
      screenHeight,
      windowWidth: Number(info.windowWidth),
      windowHeight: Number(info.windowHeight),
      pixelRatio: Number(info.pixelRatio),
      recommendedContainAspectRatio: screenWidth > 0 && screenHeight > 0
        ? screenHeight / screenWidth
        : null,
    }
  } catch (error) {
    return {
      error: error && error.message ? error.message : String(error),
    }
  }
}

function buildDeviceToMiniProgramConfig(deviceUI, phoneAspectRatio) {
  const callerUI = { ...deviceUI }
  const listenerUI = { ...deviceUI, cameraRotation: 0 }
  const screenAspectRatio = validAspectRatio(phoneAspectRatio)

  // 设备主动呼叫时，设备是 caller，手机小程序是 listener。插件的来电页面
  // 同时读取两端配置，所以 listenerUI 也要携带设备画面的缩放和镜像配置；
  // 但手机本端预览必须保持正向。
  if (deviceUI.objectFit === 'contain' && screenAspectRatio !== null) {
    callerUI.aspectRatio = screenAspectRatio
    listenerUI.aspectRatio = screenAspectRatio
  }
  return { callerUI, listenerUI }
}

function buildMiniProgramToDeviceConfig(deviceUI, phoneAspectRatio) {
  const callerUI = {
    // 小程序主动呼叫时，小程序是 caller，本机摄像头始终保持正向。
    cameraRotation: 0,
  }
  const listenerUI = {}
  const screenAspectRatio = validAspectRatio(phoneAspectRatio)

  // 通话页面比例属于手机容器，不使用设备素材的 aspectRatio。
  if (screenAspectRatio !== null) {
    callerUI.aspectRatio = screenAspectRatio
    listenerUI.aspectRatio = screenAspectRatio
  }
  if (deviceUI.cameraRotation !== undefined) {
    listenerUI.cameraRotation = deviceUI.cameraRotation
  }
  if (deviceUI.horMirror !== undefined) listenerUI.horMirror = deviceUI.horMirror
  if (deviceUI.vertMirror !== undefined) listenerUI.vertMirror = deviceUI.vertMirror
  if (deviceUI.objectFit !== undefined) listenerUI.objectFit = deviceUI.objectFit
  return { callerUI, listenerUI }
}

function buildVoipUIConfig(direction, deviceUI, phoneAspectRatio) {
  const ui = deviceUI && typeof deviceUI === 'object' ? deviceUI : {}
  if (direction === CALL_DIRECTION.DEVICE_TO_MINI_PROGRAM) {
    return buildDeviceToMiniProgramConfig(ui, phoneAspectRatio)
  }
  if (direction === CALL_DIRECTION.MINI_PROGRAM_TO_DEVICE) {
    return buildMiniProgramToDeviceConfig(ui, phoneAspectRatio)
  }
  throw new Error('不支持的 VoIP 呼叫方向: ' + String(direction))
}

module.exports = {
  CALL_DIRECTION,
  buildVoipUIConfig,
  phoneDisplaySnapshot,
}
