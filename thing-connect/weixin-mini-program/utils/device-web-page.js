const PAGES = {
  live: { path: '/player', title: '实时查看' },
  agent: { path: '/v1/ai/agent', title: 'AI 角色' },
}

function buildDeviceWebPage(baseUrl, kind, deviceId, token) {
  const page = Object.prototype.hasOwnProperty.call(PAGES, kind) && PAGES[kind]
  // Only the configured HTTPS origin and two fixed product pages may receive credentials.
  const origin = String(baseUrl || '').replace(/\/+$/, '')
  if (!/^https:\/\/[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$/i.test(origin)) {
    throw new Error('服务地址不可用，请联系管理员')
  }
  if (!page || typeof deviceId !== 'string' || !deviceId.trim()) {
    throw new Error('设备入口无效，请返回设备列表重新打开')
  }
  if (typeof token !== 'string' || !token) throw new Error('请先登录')
  return {
    title: page.title,
    // The fragment is never sent to the HTTP server or in the Referer header.
    // The receiving page consumes it before loading any other scripts.
    url: origin + page.path + '?device_id=' + encodeURIComponent(deviceId)
      + '&source=miniprogram#mini_token=' + encodeURIComponent(token),
  }
}

module.exports = { buildDeviceWebPage }
