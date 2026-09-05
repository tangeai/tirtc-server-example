/* Shared bootstrap for the two device pages embedded by the native mini program. */
(function () {
  'use strict';
  const params = new URLSearchParams(location.search);
  const embedded = params.get('source') === 'miniprogram';
  const fragment = new URLSearchParams(location.hash.slice(1));
  const suppliedToken = fragment.get('mini_token') || '';
  // Consume credentials before SDKs, external resources, or page code run.
  if (fragment.has('mini_token')) history.replaceState(null, '', location.pathname + location.search);
  const token = embedded ? suppliedToken : localStorage.getItem('token');

  function goBack() {
    if (!embedded) { location.href = '/devices'; return; }
    if (window.wx && wx.miniProgram) {
      wx.miniProgram.navigateBack();
    } else {
      alert('请点击左上角返回小程序设备列表');
    }
  }

  let sessionExpired = false;
  let returningToLogin = false;
  function returnToLogin() {
    if (returningToLogin || !window.wx || !window.wx.miniProgram) return;
    returningToLogin = true;
    window.wx.miniProgram.reLaunch({
      url: '/pages/login/index?expired=1',
      fail: function () { returningToLogin = false; },
    });
  }

  function requireLogin() {
    if (!embedded) { location.href = '/login'; return; }
    sessionExpired = true;
    returnToLogin();
    if (document.getElementById('mini-session-expired')) return;
    const panel = document.createElement('div');
    panel.id = 'mini-session-expired';
    panel.style.cssText = 'position:fixed;inset:0;z-index:99999;background:#EDEDED;color:#1A1A1A;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:24px;text-align:center;font:16px/1.8 system-ui';
    const title = document.createElement('h2');
    title.textContent = '登录状态已过期';
    const description = document.createElement('p');
    description.textContent = '请返回小程序首页，重新登录。';
    const button = document.createElement('button');
    button.textContent = '重新登录';
    button.style.cssText = 'border:0;border-radius:12px;padding:14px 28px;background:#07C160;color:white;font:inherit;margin-top:20px';
    button.onclick = returnToLogin;
    panel.append(title, description, button);
    document.body.appendChild(panel);
  }

  window.MiniProgramPage = { embedded, token, goBack, requireLogin };
  if (embedded) {
    document.documentElement.classList.add('mini-program');
    const sdk = document.createElement('script');
    sdk.src = 'https://res.wx.qq.com/open/js/jweixin-1.6.0.js';
    sdk.async = true;
    sdk.onload = function () { if (sessionExpired) returnToLogin(); };
    document.head.appendChild(sdk);
  }
})();
