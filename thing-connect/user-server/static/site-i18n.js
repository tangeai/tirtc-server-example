// Public-site UI copy only. Board catalog fields are displayed exactly as returned by the API.
(function (root) {
  'use strict';

  const storageKey = 'xiaotai.site.language';
  const english = {
    'site.brand': 'Tange · Xiaotai',
    'site.homeLabel': 'Xiaotai home',
    'site.mainNav': 'Main navigation',
    'site.menuOpen': 'Open navigation',
    'site.menuClose': 'Close navigation',
    'site.quickStart': 'Try it now',
    'site.demos': 'Demo video',
    'site.boards': 'Boards',
    'site.community': 'Community',
    'site.login': 'Log in',
    'site.register': 'Sign up',
    'site.console': 'Console',
    'home.title': 'Open-source real-time audio and video for devices | Xiaotai',
    'home.description': 'Open-source real-time audio and video for devices, including WeChat VoIP, intercom, AI, Web and device simulators.',
    'home.eyebrow': 'Device audio and video · Open source from board to application',
    'home.heroTitle': 'Build real-time audio and video for devices',
    'home.heroEmphasis': 'Open source, ready to build on',
    'home.featuresLabel': 'Core features',
    'home.featureLive': 'Live audio and video',
    'home.featureWeChat': 'WeChat VoIP calls',
    'home.featureIntercom': 'Device intercom',
    'home.featureGroup': 'Group intercom',
    'home.featureChat': 'AI chat',
    'home.featureRecognition': 'AI recognition',
    'home.heroCopy': 'Flash the firmware, connect and bind your device to try WeChat VoIP, intercom and AI chat. Reuse the open-source device, server, mini-program, Web and app components to build your product faster.',
    'home.chooseRoute': 'Choose how to start',
    'home.getSource': 'Get the source code',
    'home.heroNote': 'Compatible development boards and a desktop simulator for testing without hardware',
    'home.githubSlow': 'GitHub download too slow?',
    'home.gitee': 'Use the Gitee mirror',
    'home.mirrorDelay': '(May be slightly behind)',
    'home.terminalLabel': 'Simulator preview',
    'home.platformLabel': 'Choose an operating environment',
    'home.afterCommands': 'After the commands succeed, open the demo platform:',
    'home.simulatorConnected': 'TiRTC device simulator connected',
    'home.simulatorReady': 'Ready to try the audio and video features',
    'home.routesKicker': 'Choose your starting point',
    'home.routesTitleFirst': 'Try a development board,',
    'home.routesTitleSecond': 'deploy the app when ready',
    'home.routesIntro': 'Flash a supported board to get started, use a desktop simulator, or deploy the complete source yourself.',
    'home.recommended': 'Recommended',
    'home.boardRouteTitle': 'Flash a board and start exploring',
    'home.boardRouteCopy': 'Choose a supported board and flash its firmware. Once connected and bound, try it in the Web app or WeChat mini-program.',
    'home.boardFirmware': 'Browse boards and firmware',
    'home.simRouteTitle': 'No board? Try it on a computer',
    'home.simRouteCopy': 'Run a device simulator to try device connection, binding, and audio and video without hardware.',
    'home.simGuide': 'View simulator guide',
    'home.deployTitle': 'Deploy the complete application',
    'home.deployCopy': 'Download the source, host your own platform, and build on it for your product.',
    'home.deployParts': 'Server, Web and WeChat mini-program',
    'home.deviceSource': 'Device source code',
    'home.deployGuide': 'View deployment guide',
    'home.demoTitle': 'Feature demos',
    'home.demoIntro': 'See what a connected device can do.',
    'home.videoUnsupported': 'Your browser does not support video playback.',
    'home.playDemo': 'Play demo',
    'home.liveLabel': 'Remote live view',
    'home.liveTitle': 'View live audio and video remotely',
    'home.liveCopy': 'The device streams through TiRTC for real-time viewing in the Web demo.',
    'home.aiLabel': 'AI intercom and device calls',
    'home.aiTitle': 'AI intercom and device calls',
    'home.aiCopy': 'Talk to AI, ask it to call another device, or call a WeChat mini-program from the device.',
    'home.videoLiveAria': 'Play the remote live audio and video demo',
    'home.videoAiAria': 'Play the AI intercom and device call demo',
    'home.contact': 'Business and technical enquiries',
    'home.supportConnect': 'Scan with WeChat to contact our team',
    'home.tryMiniProgram': 'Try Xiaotai on WeChat',
    'home.supportQrAlt': 'Customer support QR code. Scan with WeChat to connect.',
    'home.qrAlt': 'Scan with WeChat or search for the Xiaotai mini-program',
    'home.company': 'Tange AI',
    'home.sdkDocs': 'TiRTC SDK docs',
    'home.copy': 'Copy',
    'home.copied': 'Copied',
    'home.copyCommand': 'Copy {platform} command',
    'home.commandCopied': 'Command copied',
    'boards.title': 'Development boards — Xiaotai',
    'boards.description': 'Explore supported development boards, firmware, source repositories, and flashing guides for Xiaotai.',
    'boards.intro': 'Boards',
    'boards.introCopy': 'Explore supported boards and related resources.',
    'boards.error': 'Could not load the catalog',
    'boards.errorCopy': 'Check your connection and try again.',
    'boards.retry': 'Reload',
    'boards.empty': 'Board information is coming soon',
    'boards.emptyCopy': 'Visit the community to suggest a board and describe how you would use it.',
    'boards.more': 'More boards',
    'boards.vendorBoard': 'Vendor / board',
    'boards.model': 'Full model',
    'boards.chipPlatform': 'Chip platform',
    'boards.capabilities': 'Capabilities',
    'boards.status': 'Status',
    'boards.requestTitle': 'Can’t find your board?',
    'boards.requestCopy': 'Submit the full model, official documentation, and intended use. We’ll review it for adaptation.',
    'boards.requestLink': 'Suggest a board',
    'boards.close': 'Close details',
    'boards.playVideo': 'Watch demo video',
    'boards.chip': 'Chip',
    'boards.vendor': 'Vendor',
    'boards.buy': 'Buy ↗',
    'boards.repository': 'Repository ↗',
    'boards.firmware': 'Firmware ↗',
    'boards.flashingGuide': 'Flashing guide ↗',
    'boards.merchantNote': 'Purchases are handled by third-party sellers. Contact the seller about products, orders, or after-sales service. Xiaotai provides information only and does not partner with, represent, or guarantee the seller.',
    'boards.ready': 'Supported',
    'boards.adapting': 'In progress',
    'boards.planned': 'Planned',
    'boards.details': 'Details →',
    'boards.productImage': '{name} product image',
    'boards.count': '{count} boards',
  };
  const chineseDynamic = {
    'site.menuOpen': '打开导航',
    'site.menuClose': '关闭导航',
    'home.copy': '复制',
    'home.copied': '已复制',
    'home.copyCommand': '复制 {platform} 运行命令',
    'home.commandCopied': '运行命令已复制',
    'boards.ready': '已适配',
    'boards.adapting': '适配中',
    'boards.planned': '计划中',
    'boards.details': '详情 →',
    'boards.productImage': '{name} 产品图',
    'boards.count': '{count} 款',
  };
  const dictionaries = { 'zh-CN': chineseDynamic, en: english };
  const listeners = new Set();
  let language = 'zh-CN';

  function savedLanguage() {
    try { return root.localStorage.getItem(storageKey); } catch (_) { return null; }
  }

  function t(key, values = {}) {
    const template = dictionaries[language][key] || dictionaries['zh-CN'][key] || key;
    return template.replace(/\{(\w+)\}/g, (_, name) => String(values[name] ?? ''));
  }

  function apply() {
    const doc = root.document;
    if (!doc) return;
    doc.documentElement.lang = language;
    doc.querySelectorAll('[data-i18n]').forEach((element) => {
      if (element.dataset.i18nOriginal === undefined) element.dataset.i18nOriginal = element.textContent;
      element.textContent = language === 'zh-CN' ? element.dataset.i18nOriginal : t(element.dataset.i18n);
    });
    for (const [attribute, selector, keyName, originalName] of [
      ['aria-label', '[data-i18n-aria-label]', 'i18nAriaLabel', 'i18nOriginalAriaLabel'],
      ['content', '[data-i18n-content]', 'i18nContent', 'i18nOriginalContent'],
      ['alt', '[data-i18n-alt]', 'i18nAlt', 'i18nOriginalAlt'],
    ]) {
      doc.querySelectorAll(selector).forEach((element) => {
        if (element.dataset[originalName] === undefined) element.dataset[originalName] = element.getAttribute(attribute) || '';
        element.setAttribute(attribute, language === 'zh-CN' ? element.dataset[originalName] : t(element.dataset[keyName]));
      });
    }
    doc.querySelectorAll('[data-site-language-toggle]').forEach((button) => {
      button.textContent = language === 'zh-CN' ? 'English' : '中文';
      button.setAttribute('aria-label', language === 'zh-CN' ? 'Switch to English' : '切换为中文');
    });
    listeners.forEach((listener) => listener(language));
  }

  function setLanguage(nextLanguage) {
    if (!dictionaries[nextLanguage]) return;
    if (root.WebI18n) { root.WebI18n.setLanguage(nextLanguage); return; }
    language = nextLanguage;
    try { root.localStorage.setItem(storageKey, language); } catch (_) { /* Storage may be disabled. */ }
    apply();
  }

  language = root.WebI18n?.getLanguage() || savedLanguage() || (/^zh\b/i.test(root.navigator?.language || '') ? 'zh-CN' : 'en');
  if (!dictionaries[language]) language = 'zh-CN';
  root.SiteI18n = { t, getLanguage: () => language, setLanguage, onChange: (listener) => listeners.add(listener) };
  if (root.WebI18n) root.WebI18n.onChange(next => { language = next; apply(); });
  if (root.document) {
    root.document.querySelectorAll('[data-site-language-toggle]').forEach((button) => {
      if (!root.WebI18n) button.addEventListener('click', () => setLanguage(language === 'zh-CN' ? 'en' : 'zh-CN'));
    });
    apply();
  }
})(typeof window === 'undefined' ? globalThis : window);
