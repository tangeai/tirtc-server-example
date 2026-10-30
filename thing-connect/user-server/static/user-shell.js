(function () {
  'use strict';
  const embedded = new URLSearchParams(location.search).get('source') === 'miniprogram';
  let redirecting = false;
  function requireLogin() {
    if (embedded) { window.MiniProgramPage?.requireLogin(); return; }
    if (redirecting) return;
    redirecting = true;
    localStorage.removeItem('token');
    sessionStorage.setItem('login-notice', WebI18n.t("登录状态已失效，请重新登录"));
    location.replace('/login');
  }
  window.UserSession = {requireLogin};
  const originalFetch = window.fetch.bind(window);
  window.fetch = async function (input, options) {
    const response = await originalFetch(input, options);
    const request = input instanceof Request ? input : null;
    const url = new URL(request ? request.url : input, location.href);
    const headers = new Headers(options?.headers || request?.headers);
    if (!embedded && url.origin === location.origin && url.pathname.startsWith('/v1/') && headers.get('Authorization')?.startsWith('Bearer ')) {
      let expired = response.status === 401;
      if (!expired && response.headers.get('content-type')?.includes('application/json')) {
        try { expired = (await response.clone().json()).code === 401; } catch { /* The caller handles malformed responses. */ }
      }
      if (expired) { requireLogin(); throw Error(WebI18n.t("登录状态已失效，请重新登录")); }
    }
    return response;
  };
  const anchor = link => {
    const a = document.createElement('a');
    WebI18n.bindText(a, () => (link.name + ' ↗'), "textContent"); a.href = link.url; a.target = '_blank'; a.rel = 'noopener noreferrer';
    a.className = 'block px-3 py-2 text-sm text-gray-500 hover:text-emerald-600 hover:bg-gray-50 rounded-lg';
    return a;
  };
  async function navigation() {
    const mount = document.querySelector('[data-navigation]');
    if (!mount) return;
    try {
      const r = await fetch('/v1/config/navigation', {signal:AbortSignal.timeout(5000),cache:'no-store'});
      const body = await r.json();
      if (!r.ok || body.code !== 200 || !Array.isArray(body.data?.links)) return;
      const links = body.data.links.filter(link => {
        try { const url = new URL(link.url);return link.enabled && typeof link.name==='string' && link.name.trim() && ['http:','https:'].includes(url.protocol) && !url.username && !url.password; } catch {return false;}
      }).slice(0,3);
      if (!links.length) return;
      const desktop = document.createElement('nav'); desktop.className='hidden md:flex items-center gap-1';desktop.setAttribute('aria-label',WebI18n.t("相关链接"));
      const mobile = document.createElement('details');mobile.className='relative md:hidden';
      const summary = document.createElement('summary');summary.className='cursor-pointer text-xs text-gray-500 px-2 py-2 list-none whitespace-nowrap';WebI18n.bindText(summary, () => (WebI18n.t("相关链接 ▾")), "textContent");
      const panel = document.createElement('nav');panel.className='absolute right-0 top-full mt-2 w-44 p-1 bg-white border border-gray-200 rounded-xl shadow-lg';panel.setAttribute('aria-label',WebI18n.t("相关链接"));
      links.forEach(link=>{desktop.append(anchor(link));panel.append(anchor(link));});
      mobile.append(summary,panel);mount.replaceChildren(desktop,mobile);mount.hidden=false;
      document.addEventListener('click',event=>{if(!mobile.contains(event.target))mobile.open=false;});
      mobile.addEventListener('keydown',event=>{if(event.key==='Escape'){mobile.open=false;summary.focus();}});
    } catch { /* Optional navigation stays hidden when unavailable. */ }
  }
  async function accountMenu() {
    const trigger=document.getElementById('user-menu-trigger'),menu=document.getElementById('user-menu');
    if(!trigger||!menu)return;
    const close=()=>{menu.hidden=true;trigger.setAttribute('aria-expanded','false');};
    trigger.onclick=()=>{menu.hidden=!menu.hidden;trigger.setAttribute('aria-expanded',String(!menu.hidden));};
    document.addEventListener('click',event=>{if(!menu.contains(event.target)&&!trigger.contains(event.target))close();});
    document.addEventListener('keydown',event=>{if(event.key==='Escape'&&!menu.hidden){close();trigger.focus();}});
    document.getElementById('user-logout').onclick=()=>{localStorage.removeItem('token');sessionStorage.removeItem('login-notice');location.replace('/login');};
    const label=document.getElementById('user-account');
    const retry=document.getElementById('user-account-retry');
    const load=async()=>{
      retry.hidden=true;WebI18n.bindText(label, () => (WebI18n.t("正在加载账号…")), "textContent");
      try {
        const r=await fetch('/v1/user/me',{headers:{Authorization:'Bearer '+localStorage.getItem('token')},signal:AbortSignal.timeout(5000),cache:'no-store'});
        const body=await r.json();if(!r.ok||body.code!==200||!body.data?.email)throw Error();
        WebI18n.bindText(label, () => (body.data.email), "textContent");
        WebI18n.bindText(document.getElementById('user-avatar'), () => (Array.from(body.data.email)[0].toUpperCase()), "textContent");
      }catch{WebI18n.bindText(label, () => (WebI18n.t("账号信息加载失败")), "textContent");retry.hidden=false;}
    };
    retry.onclick=load;
    await load();
  }
  document.addEventListener('DOMContentLoaded',()=>{navigation();accountMenu();});
})();
