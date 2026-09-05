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

  function requireLogin() {
    if (!embedded) { location.href = '/login'; return; }
    if (document.getElementById('mini-session-expired')) return;
    const panel = document.createElement('div');
    panel.id = 'mini-session-expired';
    panel.style.cssText = 'position:fixed;inset:0;z-index:99999;background:#EDEDED;color:#1A1A1A;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:24px;text-align:center;font:16px/1.8 system-ui';
    const title = document.createElement('h2');
    title.textContent = '请重新打开页面';
    const description = document.createElement('p');
    description.textContent = '当前登录状态已失效，请返回小程序后重试。';
    const button = document.createElement('button');
    button.textContent = '返回设备列表';
    button.style.cssText = 'border:0;border-radius:12px;padding:14px 28px;background:#07C160;color:white;font:inherit;margin-top:20px';
    button.onclick = goBack;
    panel.append(title, description, button);
    document.body.appendChild(panel);
  }

  window.MiniProgramPage = { embedded, token, goBack, requireLogin };
  if (embedded) {
    document.documentElement.classList.add('mini-program');
    const sdk = document.createElement('script');
    sdk.src = 'https://res.wx.qq.com/open/js/jweixin-1.6.0.js';
    sdk.async = true;
    document.head.appendChild(sdk);
  }
})();
