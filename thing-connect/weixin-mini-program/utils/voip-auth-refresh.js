async function refreshVoipAuthState(tasks) {
  const {
    refreshOpenId,
    syncWechatAuthState,
    syncContactRemark,
    syncServerAuthList,
    reconcileAuthorizedDevices,
  } = tasks

  // 每轮页面刷新只强制更新一次微信登录关系。失败时仍继续读取微信本地
  // 授权状态，但不让后续服务端同步各自再次触发 wx.login。
  let openIdReady = true
  try {
    await refreshOpenId()
  } catch (_) {
    openIdReady = false
  }

  await syncWechatAuthState()
  if (!openIdReady) {
    return { serverAuthListLoaded: false, serverAuthChanged: false }
  }

  await syncContactRemark()
  const serverAuthListLoaded = await syncServerAuthList()
  if (!serverAuthListLoaded) {
    return { serverAuthListLoaded: false, serverAuthChanged: false }
  }

  const serverAuthChanged = !!(await reconcileAuthorizedDevices())
  if (serverAuthChanged) {
    // 只有 report-auth/delete-auth 确实成功后才重新读取最终快照。
    await syncServerAuthList()
  }
  return { serverAuthListLoaded: true, serverAuthChanged }
}

module.exports = { refreshVoipAuthState }
