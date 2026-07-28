const DEVICE_VIDEO_PROFILE_CACHE_KEY = 'voip_device_video_profiles'

function normalizeCameraRotation(value) {
  if (value === null || value === undefined) return null
  if (typeof value === 'string' && !value.trim()) return null
  const rotation = Number(value)
  return [0, 90, 180, 270].includes(rotation) ? rotation : null
}

function normalizeAspectRatio(value) {
  const ratio = Number(value)
  return Number.isFinite(ratio) && ratio > 0 ? ratio : null
}

function normalizeBoolean(value) {
  if (value === true || value === 'true') return true
  if (value === false || value === 'false') return false
  return null
}

function normalizeObjectFit(value) {
  const objectFit = String(value || '').trim().toLowerCase()
  return ['fill', 'contain'].includes(objectFit) ? objectFit : null
}

function firstDefined(object, snakeName, camelName) {
  if (!object) return undefined
  if (Object.prototype.hasOwnProperty.call(object, snakeName)) return object[snakeName]
  return object[camelName]
}

function normalizedValue(primary, fallback, snakeName, camelName, normalize) {
  const direct = normalize(firstDefined(primary, snakeName, camelName))
  if (direct !== null) return direct
  return normalize(firstDefined(fallback, snakeName, camelName))
}

function buildVideoUIConfig(primary, fallback) {
  const ui = {}
  const rotation = normalizedValue(
    primary, fallback, 'camera_rotation', 'cameraRotation', normalizeCameraRotation
  )
  const aspectRatio = normalizedValue(
    primary, fallback, 'aspect_ratio', 'aspectRatio', normalizeAspectRatio
  )
  const horMirror = normalizedValue(
    primary, fallback, 'hor_mirror', 'horMirror', normalizeBoolean
  )
  const vertMirror = normalizedValue(
    primary, fallback, 'vert_mirror', 'vertMirror', normalizeBoolean
  )
  const objectFit = normalizedValue(
    primary, fallback, 'object_fit', 'objectFit', normalizeObjectFit
  )
  if (rotation !== null) ui.cameraRotation = rotation
  if (aspectRatio !== null) ui.aspectRatio = aspectRatio
  if (horMirror !== null) ui.horMirror = horMirror
  if (vertMirror !== null) ui.vertMirror = vertMirror
  if (objectFit !== null) ui.objectFit = objectFit
  return ui
}

function incomingDeviceID(query) {
  const value = firstDefined(query, 'device_id', 'deviceId')
  return String(
    value
      || (query && (query.callerId || query.caller_id || query.sn))
      || '',
  ).trim()
}

function cachedProfile(deviceID) {
  if (!deviceID) return null
  try {
    const profiles = wx.getStorageSync(DEVICE_VIDEO_PROFILE_CACHE_KEY)
    return profiles && typeof profiles === 'object' ? profiles[deviceID] || null : null
  } catch (_) {
    return null
  }
}

function updateDeviceVideoProfileCache(devices) {
  const profiles = {}
  ;(Array.isArray(devices) ? devices : []).forEach((device) => {
    const deviceID = String((device && device.device_id) || '').trim()
    if (!deviceID) return
    profiles[deviceID] = {
      device_id: deviceID,
      camera_rotation: normalizeCameraRotation(device.camera_rotation),
      aspect_ratio: normalizeAspectRatio(device.aspect_ratio),
      hor_mirror: normalizeBoolean(device.hor_mirror),
      vert_mirror: normalizeBoolean(device.vert_mirror),
      object_fit: normalizeObjectFit(device.object_fit),
    }
  })
  try {
    wx.setStorageSync(DEVICE_VIDEO_PROFILE_CACHE_KEY, profiles)
  } catch (error) {
    console.warn('更新设备视频 profile 缓存失败', error)
  }
  return profiles
}

module.exports = {
  DEVICE_VIDEO_PROFILE_CACHE_KEY,
  buildVideoUIConfig,
  cachedProfile,
  incomingDeviceID,
  normalizeAspectRatio,
  normalizeBoolean,
  normalizeCameraRotation,
  normalizeObjectFit,
  updateDeviceVideoProfileCache,
}
