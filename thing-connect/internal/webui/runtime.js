(function (root) {
  'use strict';
  const config = root.WebLanguageConfig;
  const storageKey = 'xiaotai.site.language';
  const messages = Object.create(null);
  const listeners = new Set();
  const bindings = new Map();
  let saved;
  try { saved = root.localStorage.getItem(storageKey); } catch (_) { /* Storage is optional. */ }
  const valid = value => value === 'en' || value === 'zh-CN';
  let language = config.allow_switch && valid(saved) ? saved : config.default;
  if (!valid(language)) language = /^zh\b/i.test(root.navigator?.language || '') ? 'zh-CN' : 'en';
  function t(key, params = {}) {
    const text = language === 'en' ? messages[key] ?? key : key;
    return text.replace(/\{(\w+)\}/g, (match, name) => Object.hasOwn(params, name) ? String(params[name]) : match);
  }
  const escape = value => String(value).replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));
  function mark(key, params = {}) {
    return '<span data-ui-i18n="' + escape(key) + '" data-ui-params="' + escape(JSON.stringify(params)) + '">' + escape(t(key, params)) + '</span>';
  }
  function bindText(element, read, attribute = 'textContent') {
    // Explicit bindings update only UI output; input values and user content are never scanned.
    const value = read();
    element.removeAttribute?.('data-ui-i18n');
    if (element === root.document && attribute === 'title') root.document.querySelector?.('title')?.removeAttribute('data-ui-i18n');
    let fields = bindings.get(element);
    if (!fields) { fields = new Map(); bindings.set(element, fields); }
    fields.set(attribute, {read, last: value});
    element[attribute] = value;
    return value;
  }
  function translate(scope) {
    if (!scope?.querySelectorAll) return;
    const nodes = [...scope.querySelectorAll('[data-ui-i18n], [data-ui-attrs]')];
    if (scope.matches?.('[data-ui-i18n], [data-ui-attrs]')) nodes.unshift(scope);
    nodes.forEach(element => {
      let params = {};
      try { params = JSON.parse(element.dataset.uiParams || '{}'); } catch (_) { /* Invalid optional parameters are ignored. */ }
      if (element.hasAttribute('data-ui-i18n')) {
        const value = t(element.dataset.uiI18n, params);
        if (element.textContent !== value) element.textContent = value;
      }
      if (element.dataset.uiAttrs) {
        const attributes = JSON.parse(element.dataset.uiAttrs);
        Object.entries(attributes).forEach(([name, key]) => {
          const value = t(key, params);
          if (element.getAttribute(name) !== value) element.setAttribute(name, value);
        });
      }
    });
  }
  function toggleLabel(button) {
    button.hidden = !config.allow_switch;
    button.style?.setProperty('display', config.allow_switch ? '' : 'none', config.allow_switch ? '' : 'important');
    button.disabled = !config.allow_switch;
    button.textContent = language === 'en' ? '中文' : 'English';
    button.setAttribute('aria-label', language === 'en' ? 'Switch to Chinese' : 'Switch to English');
  }
  function apply() {
    const doc = root.document;
    if (!doc) return;
    doc.documentElement.lang = language;
    translate(doc);
    bindings.forEach((fields, element) => {
      if (element !== root.document && !element.isConnected) { bindings.delete(element); return; }
      fields.forEach((binding, attribute) => {
        if (element[attribute] !== binding.last) { fields.delete(attribute); return; }
        binding.last = binding.read();
        element[attribute] = binding.last;
      });
    });
    doc.querySelectorAll('[data-language-toggle], [data-site-language-toggle]').forEach(toggleLabel);
  }
  function setLanguage(next) {
    if (!config.allow_switch || !valid(next) || language === next) return;
    language = next;
    try { root.localStorage.setItem(storageKey, language); } catch (_) { /* Storage may be disabled. */ }
    apply();
    listeners.forEach(listener => listener(language));
  }
  function errorMessage(body, fallback = '操作失败，请重试') {
    if (language !== 'en') return body?.msg || t(fallback);
    const key = errorKeys[body?.code];
    return t(key || fallback);
  }
  const errorKeys = {
    401:'登录状态已失效，请重新登录', 40000:'请求参数不正确，请检查后重试',
    4090:'邮箱已被注册', 4091:'邮箱或密码错误', 4002:'验证码错误或已过期', 4003:'设备额度已用尽，请联系管理员扩容',
    40012:'人机验证失败，请重试',
    40013:'验证码错误或已过期',
    40300:'无权操作该资源', 40400:'资源不存在，请刷新后重试', 4040:'设备 ID 不存在或不在您的账号配额内',
    40920:'房间人数已满', 40921:'房间状态已变化，请刷新后重试',
    40923:'设备已加入其他房间，请先退出', 40320:'房间号或密码错误',
    429:'请求过于频繁，请稍后重试', 42920:'尝试次数过多，请稍后重试',
    50000:'服务暂时不可用，请稍后重试', 50200:'服务暂时不可用，请稍后重试',
    5001:'网络波动，下发超时，请稍后重试（配额已保留）',
    6002:'设备已离线，请重新触发设备获取验证码',
    6004:'该设备已被其他账号绑定，若需使用请联系管理员重置',
    6006:'设备已解绑',
  };
  root.WebI18n = {t, mark, bindText, translate, errorMessage,
    clearLocalSession: () => {
      const preference = root.localStorage.getItem(storageKey);
      root.localStorage.clear();
      if (valid(preference)) root.localStorage.setItem(storageKey, preference);
    },
    getLanguage: () => language,
    locale: () => language === 'en' ? 'en-US' : 'zh-CN',
    setLanguage, onChange: listener => { listeners.add(listener); return () => listeners.delete(listener); },
    addMessages: value => Object.assign(messages, value),
  };
  root.document.documentElement.lang = language;
  root.document.addEventListener('DOMContentLoaded', () => {
    const doc = root.document;
    doc.querySelectorAll('[data-language-toggle], [data-site-language-toggle]').forEach(button => {
      button.addEventListener('click', () => setLanguage(language === 'en' ? 'zh-CN' : 'en'));
    });
    apply();
    // Only explicitly marked copy is observed; this does not reload data or rebuild views.
    const observer = new MutationObserver(records => {
      records.forEach(record => record.addedNodes.forEach(node => translate(node)));
      bindings.forEach((_, element) => { if (element !== root.document && !element.isConnected) bindings.delete(element); });
    });
    observer.observe(doc.body, {childList:true, subtree:true});
  });
  root.addEventListener('storage', event => {
    if (event.key !== storageKey || !config.allow_switch || !valid(event.newValue)) return;
    language = event.newValue; apply(); listeners.forEach(listener => listener(language));
  });
})(window);
