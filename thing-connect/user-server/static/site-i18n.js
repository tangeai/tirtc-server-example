// Public-site UI copy only. Board catalog fields are displayed exactly as returned by the API.
(function (root) {
  "use strict";

  const storageKey = "xiaotai.site.language";
  const english = {
    "site.brand": "Tange · Xiaotai",
    "site.homeLabel": "Xiaotai home",
    "site.mainNav": "Main navigation",
    "site.menuOpen": "Open navigation",
    "site.menuClose": "Close navigation",
    "site.quickStart": "Try it now",
    "site.demos": "Demo video",
    "site.boards": "Boards",
    "site.community": "Community",
    "site.login": "Log in",
    "site.register": "Sign up",
    "site.console": "Console",
    "home.title": "Open-source real-time audio and video for devices | Xiaotai",
    "home.description":
      "Open-source real-time audio and video for devices, including WeChat VoIP, intercom, AI, Web and device simulators.",
    "home.eyebrow":
      "Device audio and video · Open source from board to application",
    "home.heroTitle": "Build real-time audio and video for devices",
    "home.heroEmphasis": "Open source, ready to build on",
    "home.featuresLabel": "Core features",
    "home.featureLive": "Live audio and video",
    "home.featureWeChat": "WeChat VoIP calls",
    "home.featureIntercom": "Device intercom",
    "home.featureGroup": "Group intercom",
    "home.featureChat": "AI chat",
    "home.featureRecognition": "AI recognition",
    "home.heroCopy":
      "Flash the firmware, connect and bind your device to try WeChat VoIP, intercom and AI chat. Reuse the open-source device, server, mini-program, Web and app components to build your product faster.",
    "home.chooseRoute": "Choose how to start",
    "home.getSource": "Get the source code",
    "home.heroNote":
      "Compatible development boards and a desktop simulator for testing without hardware",
    "home.githubSlow": "GitHub download too slow?",
    "home.gitee": "Use the Gitee mirror",
    "home.mirrorDelay": "(May be slightly behind)",
    "home.terminalLabel": "Simulator preview",
    "home.platformLabel": "Choose an operating environment",
    "home.afterCommands": "After the commands succeed, open the demo platform:",
    "home.simulatorConnected": "TiRTC device simulator connected",
    "home.simulatorReady": "Ready to try the audio and video features",
    "home.routesKicker": "Choose your starting point",
    "home.routesTitleFirst": "Try a development board,",
    "home.routesTitleSecond": "deploy the app when ready",
    "home.routesIntro":
      "Flash a supported board to get started, use a desktop simulator, or deploy the complete source yourself.",
    "home.recommended": "Recommended",
    "home.boardRouteTitle": "Flash a board and start exploring",
    "home.boardRouteCopy":
      "Choose a supported board and flash its firmware. Once connected and bound, try it in the Web app or WeChat mini-program.",
    "home.boardFirmware": "Browse boards and firmware",
    "home.simRouteTitle": "No board? Try it on a computer",
    "home.simRouteCopy":
      "Run a device simulator to try device connection, binding, and audio and video without hardware.",
    "home.simGuide": "View simulator guide",
    "home.deployTitle": "Deploy the complete application",
    "home.deployCopy":
      "Download the source, host your own platform, and build on it for your product.",
    "home.deployParts": "Server, Web and WeChat mini-program",
    "home.deviceSource": "Device source code",
    "home.deployGuide": "View deployment guide",
    "home.demoTitle": "Feature demos",
    "home.demoIntro": "See what a connected device can do.",
    "home.videoUnsupported": "Your browser does not support video playback.",
    "home.playDemo": "Play demo",
    "home.liveLabel": "Remote live view",
    "home.liveTitle": "View live audio and video remotely",
    "home.liveCopy":
      "The device streams through TiRTC for real-time viewing in the Web demo.",
    "home.aiLabel": "AI intercom and device calls",
    "home.aiTitle": "AI intercom and device calls",
    "home.aiCopy":
      "Talk to AI, ask it to call another device, or call a WeChat mini-program from the device.",
    "home.videoLiveAria": "Play the remote live audio and video demo",
    "home.videoAiAria": "Play the AI intercom and device call demo",
    "home.contact": "Business and technical enquiries",
    "home.supportConnect": "Scan with WeChat to contact our team",
    "home.tryMiniProgram": "Try Xiaotai on WeChat",
    "home.supportQrAlt":
      "Customer support QR code. Scan with WeChat to connect.",
    "home.qrAlt": "Scan with WeChat or search for the Xiaotai mini-program",
    "home.company": "Tange AI",
    "home.sdkDocs": "TiRTC SDK docs",
    "home.copy": "Copy",
    "home.copied": "Copied",
    "home.copyCommand": "Copy {platform} command",
    "home.commandCopied": "Command copied",
    "boards.title": "Development boards — Xiaotai",
    "boards.description":
      "Explore supported development boards, firmware, source repositories, and flashing guides for Xiaotai.",
    "boards.intro": "Boards",
    "boards.introCopy": "Explore supported boards and related resources.",
    "boards.error": "Could not load the catalog",
    "boards.errorCopy": "Check your connection and try again.",
    "boards.retry": "Reload",
    "boards.empty": "Board information is coming soon",
    "boards.emptyCopy":
      "Visit the community to suggest a board and describe how you would use it.",
    "boards.more": "More boards",
    "boards.vendorBoard": "Vendor / board",
    "boards.model": "Full model",
    "boards.chipPlatform": "Chip platform",
    "boards.capabilities": "Capabilities",
    "boards.status": "Status",
    "boards.requestTitle": "Can’t find your board?",
    "boards.requestCopy":
      "Submit the full model, official documentation, and intended use. We’ll review it for adaptation.",
    "boards.requestLink": "Suggest a board",
    "boards.close": "Close details",
    "boards.playVideo": "Watch demo video",
    "boards.chip": "Chip",
    "boards.vendor": "Vendor",
    "boards.buy": "Buy board ↗",
    "boards.repository": "View source code ↗",
    "boards.firmware": "Download firmware ↗",
    "boards.flashingGuide": "Flashing guide ↗",
    "boards.merchantNote":
      "Purchases are handled by third-party sellers. Contact the seller about products, orders, or after-sales service. Xiaotai provides information only and does not partner with, represent, or guarantee the seller.",
    "boards.ready": "Supported",
    "boards.adapting": "In progress",
    "boards.planned": "Planned",
    "boards.details": "Details →",
    "boards.productImage": "{name} product image",
    "boards.count": "{count} boards",
    "boards.countOne": "1 board",
  };
  const chineseDynamic = {
    "site.brand": "探鸽 · 小钛",
    "site.menuOpen": "打开导航",
    "site.menuClose": "关闭导航",
    "home.copy": "复制",
    "home.copied": "已复制",
    "home.copyCommand": "复制 {platform} 运行命令",
    "home.commandCopied": "运行命令已复制",
    "boards.ready": "已适配",
    "boards.adapting": "适配中",
    "boards.planned": "计划中",
    "boards.details": "详情 →",
    "boards.productImage": "{name} 产品图",
    "boards.count": "{count} 款",
    "boards.countOne": "1 款",
  };
  Object.assign(english, {
    "landing.connectionPending": "Connecting",
    "landing.connectionDuplex": "Two-way audio & video",
    "landing.connectionAI": "Voice input ⇄ AI reply",
    "landing.connectionCall": "Device A ⇄ Device B",
    "landing.connectionLive": "Video uplink · Voice downlink",
    "landing.heroKicker": "Open-source AI, audio & video firmware",
    "landing.heroTitle": "Connect your hardware to",
    "landing.heroAccent": "AI and real-time audio & video",
    "landing.heroCopy":
      "One firmware for signaling, codecs and server integration.",
    "landing.heroSteps": "Flash and go. Fully open source.",
    "landing.readyFirmware": "Ready-to-flash firmware",
    "landing.fullStack": "Device-to-app examples",
    "landing.openSource": "Open source, extensible",
    "landing.chooseBoard": "Choose a board · Get started",
    "landing.explore": "Explore the features ↓",
    "landing.noBoard": "No board? Try the desktop simulator →",
    "landing.chat": "AI chat",
    "landing.wechat": "WeChat calls",
    "landing.intercom": "Intercom",
    "landing.live": "Live audio & video",
    "landing.calling": "Calling WeChat",
    "landing.outgoing": "Device → WeChat",
    "landing.illustration": "Feature illustration",
    "landing.notLive": "/ not a live feed",
    "landing.platforms": "TiRTC SDK platforms",
    "landing.espressif": "Espressif ESP32-S3 / P4 / C3",
    "landing.beken": "Beken BK7258",
    "landing.eigencomm": "Eigencomm EC71X",
    "landing.rockchip": "Rockchip RV / RK",
    "landing.ingenic": "Ingenic T series",
    "landing.allwinner": "Allwinner V series",
    "landing.hisilicon": "HiSilicon HI3516",
    "landing.sigmastar": "SigmaStar SSC / SSD",
    "landing.allPlatforms": "Major chip platforms · View all →",
    "landing.boardsKicker": "Choose a board · Get started",
    "landing.boardsTitle": "Find your board. Get the firmware.",
    "landing.boardsHint": "Match the firmware to the exact board model.",
    "landing.moreBoards": "More boards →",
    "landing.fourSteps": "Get started in four steps",
    "landing.step1": "Choose a board",
    "landing.step1Hint": "Check the model and revision",
    "landing.step2": "Download and flash",
    "landing.step2Hint": "Connect via USB and follow the guide",
    "landing.step3": "Connect and bind",
    "landing.step3Hint": "Connect, then enter the binding code",
    "landing.step4": "Try the features",
    "landing.step4Hint": "Try AI, calls and live view",
    "landing.featuresKicker": "Core capabilities",
    "landing.featuresTitle": "Four capabilities for your product.",
    "landing.featuresHint":
      "Example uses; features depend on hardware, firmware and services.",
    "landing.aiTitle": "AI voice conversations",
    "landing.aiCopy": "Ask a question and hear the device answer.",
    "landing.aiUses":
      "AI toys · Companion robots · Desktop assistants · Smart panels · Audio guides",
    "landing.wechatTitle": "Device-to-WeChat calls",
    "landing.wechatCopy": "Make and receive calls between devices and WeChat.",
    "landing.wechatUses":
      "Video doorbells · Entry panels · Kids’ watches · Senior call terminals · Companion robots",
    "landing.intercomTitle": "Device calls & group intercom",
    "landing.intercomCopy":
      "Call another device or talk together in a group room.",
    "landing.intercomUses":
      "Entry and indoor panels · Building intercom · Home calls · Site staff · Team intercom",
    "landing.liveTitle": "Remote live audio & video",
    "landing.liveCopy": "See the device’s view, listen and talk remotely.",
    "landing.liveUses":
      "Pet cameras · Home care · Inspection robots · Remote maintenance · Unattended terminals",
    "landing.developKicker": "From demo to development",
    "landing.developTitle": "Try it. Then build on it.",
    "landing.simTitle": "Desktop simulator",
    "landing.simCopy": "Test the connection flow without hardware.",
    "landing.simExpand": "Open quick start ↓",
    "landing.sourceTitle": "Build your server and clients",
    "landing.sourceCopy":
      "Host your services and customize Web and WeChat clients.",
    "landing.sourceLink": "Get server & client source ↗",
    "landing.simQuick": "Desktop quick start",
    "landing.simQuickCopy": "Run a simulator to get started without a board.",
    "landing.windowsPrereq":
      "Windows 10/11 x64 · PowerShell. Install Git and Python 3.14, then reopen the terminal.",
    "landing.prepare": "1. Prepare the environment",
    "landing.run": "2. Download and start",
    "landing.copy": "Copy commands",
    "landing.guide": "Full guide & troubleshooting ↗",
    "landing.cPrereq": "Ubuntu x64 · Linux C file-media example",
    "landing.macPrereq":
      "Apple silicon · Install Git and CPython 3.10–3.14 first.",
    "landing.bindTitle": "3. Bind and view the stream",
    "landing.bindCopy":
      "Keep the terminal running → Log in → Enter the six-digit binding code → Open the device to view its stream.",
    "landing.openDemo": "Open demo platform ↗",
    "landing.fileMedia":
      "The default mode uses bundled media files, not your camera or microphone. See the full guide for live capture on Windows.",
    "landing.loading": "Loading boards…",
    "landing.empty": "No boards published yet",
    "landing.failed": "Could not load boards. Please try again.",
    "landing.firmware": "Get firmware ↓",
    "landing.flashGuide": "Flashing guide ↗",
    "landing.pendingLinks": "Firmware and guide coming soon",
    "landing.copied": "Commands copied.",
    "landing.copyFallback":
      "Press Ctrl+C or Command+C to copy the selected commands.",
    "landing.aiAnswer": "AI is answering",
    "landing.aiReply": "Speak and hear AI reply",
    "landing.connected": "WeChat call connected",
    "landing.twoWay": "Device ⇄ WeChat",
    "landing.intercomStatus": "Two-way device intercom",
    "landing.endpoints": "Device A ⇄ Device B",
    "landing.preview": "Remote live view",
    "landing.previewCopy": "Watch · Listen · Talk",
    "landing.question": "Say hello, please.",
    "landing.answer": "Hello! Nice to meet you.",
    "landing.wechatUser": "WeChat user",
    "landing.deviceA": "Device A",
    "landing.deviceB": "Device B",
    "landing.group": "Two-way voice · Group intercom",
    "landing.channel": "CAM 01 · Entrance",
    "landing.liveBadge": "● LIVE",
    "landing.sampleTime": "14:32:08 · DEMO",
    "landing.homeAria": "Feature preview",
    "landing.envAria": "Runtime environment",
  });
  Object.assign(chineseDynamic, {
    "landing.connectionPending": "正在建立连接",
    "landing.connectionDuplex": "双向音视频",
    "landing.connectionAI": "语音提问 ⇄ AI 回复",
    "landing.connectionCall": "设备 A ⇄ 设备 B",
    "landing.connectionLive": "视频上行 · 语音下行",
    "landing.heroKicker": "开源 AI 音视频固件",
    "landing.heroTitle": "给你的硬件接入",
    "landing.heroAccent": "AI 对话与实时音视频",
    "landing.heroCopy": "一套固件搞定信令、编解码和服务端。",
    "landing.heroSteps": "烧录即用，代码全开源。",
    "landing.readyFirmware": "固件即用",
    "landing.fullStack": "多端配套",
    "landing.openSource": "开源可扩展",
    "landing.chooseBoard": "选择开发板 · 开始体验",
    "landing.explore": "看看能做什么 ↓",
    "landing.noBoard": "没有开发板？试试电脑模拟器 →",
    "landing.chat": "AI 聊天",
    "landing.wechat": "微信通话",
    "landing.intercom": "设备对讲",
    "landing.live": "实时音视频",
    "landing.calling": "正在呼叫微信",
    "landing.outgoing": "设备 → 微信用户",
    "landing.illustration": "功能动画示意",
    "landing.notLive": "/ 非实际运行画面",
    "landing.platforms": "TiRTC SDK 平台",
    "landing.espressif": "乐鑫 ESP32-S3 / P4 / C3",
    "landing.beken": "博通集成 BK7258",
    "landing.eigencomm": "移芯 EC71X",
    "landing.rockchip": "瑞芯微 RV / RK",
    "landing.ingenic": "君正 T 系列",
    "landing.allwinner": "全志 V 系列",
    "landing.hisilicon": "海思 HI3516",
    "landing.sigmastar": "星宸 SSC / SSD",
    "landing.allPlatforms": "覆盖主流芯片平台 · 查看全部 →",
    "landing.boardsKicker": "选择开发板 · 开始体验",
    "landing.boardsTitle": "选开发板，下载固件。",
    "landing.boardsHint": "请按完整型号选择固件。",
    "landing.moreBoards": "更多开发板 →",
    "landing.fourSteps": "四步完成首次体验",
    "landing.step1": "选择开发板",
    "landing.step1Hint": "核对型号与版本",
    "landing.step2": "下载并烧录",
    "landing.step2Hint": "USB 连接，按指南烧录",
    "landing.step3": "联网并绑定",
    "landing.step3Hint": "联网后输入绑定码",
    "landing.step4": "开始体验",
    "landing.step4Hint": "体验 AI、通话与实时预览",
    "landing.featuresKicker": "核心能力",
    "landing.featuresTitle": "四大能力，接入你的产品。",
    "landing.featuresHint": "以下为参考场景，功能以硬件、固件与服务配置为准。",
    "landing.aiTitle": "AI 语音对话",
    "landing.aiCopy": "用户开口提问，设备语音回答。",
    "landing.aiUses": "AI 玩具 · 陪伴机器人 · 桌面助手 · 智能中控 · 语音导览",
    "landing.wechatTitle": "设备与微信通话",
    "landing.wechatCopy": "设备与微信用户，双向呼叫通话。",
    "landing.wechatUses":
      "可视门铃 · 门禁机 · 儿童手表 · 老人通话终端 · 陪伴机器人",
    "landing.intercomTitle": "设备互呼与多人对讲",
    "landing.intercomCopy": "两台设备互相通话，多人入房对讲。",
    "landing.intercomUses":
      "门禁机与室内屏 · 楼宇对讲 · 家庭互呼 · 园区值守 · 团队对讲",
    "landing.liveTitle": "远程实时音视频",
    "landing.liveCopy": "远程查看设备画面，听现场、说话。",
    "landing.liveUses":
      "宠物摄像头 · 家庭看护 · 巡检机器人 · 远程运维 · 无人值守终端",
    "landing.developKicker": "从体验到开发",
    "landing.developTitle": "体验之后，开始开发。",
    "landing.simTitle": "电脑模拟器",
    "landing.simCopy": "无需硬件，先跑通接入流程。",
    "landing.simExpand": "展开快速开始 ↓",
    "landing.sourceTitle": "服务端与客户端二次开发",
    "landing.sourceCopy": "自建服务，定制 Web 与微信小程序。",
    "landing.sourceLink": "获取服务端与客户端源码 ↗",
    "landing.simQuick": "电脑快速体验",
    "landing.simQuickCopy": "无需开发板，运行模拟器即可开始。",
    "landing.windowsPrereq":
      "Windows 10/11 x64 · PowerShell；先安装 Git 与 Python 3.14，安装后重新打开终端。",
    "landing.prepare": "1. 准备环境",
    "landing.run": "2. 下载并启动",
    "landing.copy": "复制命令",
    "landing.guide": "完整指南与故障排查 ↗",
    "landing.cPrereq": "Ubuntu x64 · Linux C 文件媒体示例",
    "landing.macPrereq": "Apple 芯片 · 请先安装 Git 与 CPython 3.10–3.14。",
    "landing.bindTitle": "3. 绑定并查看画面",
    "landing.bindCopy":
      "保持终端运行 → 登录体验平台 → 输入六位绑定码 → 打开设备查看音视频。",
    "landing.openDemo": "打开体验平台 ↗",
    "landing.fileMedia":
      "默认播放仓库自带素材，不使用电脑摄像头或麦克风。Windows 实时采集方式见完整指南。",
    "landing.loading": "正在加载开发板…",
    "landing.empty": "暂无已上架开发板",
    "landing.failed": "开发板加载失败，请重试。",
    "landing.firmware": "下载固件 ↓",
    "landing.flashGuide": "烧录指南 ↗",
    "landing.pendingLinks": "固件与指南待发布",
    "landing.copied": "命令已复制。",
    "landing.copyFallback": "请按 Ctrl+C 或 Command+C 复制已选中的命令。",
    "landing.aiAnswer": "AI 正在回答",
    "landing.aiReply": "你说一句，AI 回一句",
    "landing.connected": "微信通话已接通",
    "landing.twoWay": "设备 ⇄ 微信用户",
    "landing.intercomStatus": "设备双向对讲",
    "landing.endpoints": "设备 A ⇄ 设备 B",
    "landing.preview": "远程实时预览",
    "landing.previewCopy": "看画面 · 听声音 · 语音对讲",
    "landing.question": "小钛，打个招呼吧。",
    "landing.answer": "你好，很高兴认识你！",
    "landing.wechatUser": "微信用户",
    "landing.deviceA": "设备 A",
    "landing.deviceB": "设备 B",
    "landing.group": "双向语音 · 多人对讲",
    "landing.channel": "CAM 01 · 门口",
    "landing.liveBadge": "● 实时",
    "landing.sampleTime": "14:32:08 · 示例",
    "landing.homeAria": "功能展示",
    "landing.envAria": "运行环境",
  });
  const dictionaries = { "zh-CN": chineseDynamic, en: english };
  const listeners = new Set();
  let language = "zh-CN";
  let brand = null;

  function savedLanguage() {
    try {
      return root.localStorage.getItem(storageKey);
    } catch (_) {
      return null;
    }
  }

  function t(key, values = {}) {
    if (key === "site.brand" && brand)
      return language === "en" ? brand.name_en || brand.name : brand.name;
    const template =
      dictionaries[language][key] || dictionaries["zh-CN"][key] || key;
    return template.replace(/\{(\w+)\}/g, (_, name) =>
      String(values[name] ?? ""),
    );
  }

  function apply() {
    const doc = root.document;
    if (!doc) return;
    doc.documentElement.lang = language;
    doc.querySelectorAll("[data-i18n]").forEach((element) => {
      if (element.dataset.i18nOriginal === undefined)
        element.dataset.i18nOriginal = element.textContent;
      element.textContent =
        element.dataset.i18n === "site.brand" && brand
          ? t("site.brand")
          : language === "zh-CN"
            ? element.dataset.i18nOriginal
            : t(element.dataset.i18n);
    });
    for (const [attribute, selector, keyName, originalName] of [
      [
        "aria-label",
        "[data-i18n-aria-label]",
        "i18nAriaLabel",
        "i18nOriginalAriaLabel",
      ],
      ["content", "[data-i18n-content]", "i18nContent", "i18nOriginalContent"],
      ["alt", "[data-i18n-alt]", "i18nAlt", "i18nOriginalAlt"],
    ]) {
      doc.querySelectorAll(selector).forEach((element) => {
        if (element.dataset[originalName] === undefined)
          element.dataset[originalName] = element.getAttribute(attribute) || "";
        element.setAttribute(
          attribute,
          language === "zh-CN"
            ? element.dataset[originalName]
            : t(element.dataset[keyName]),
        );
      });
    }
    doc.querySelectorAll("[data-site-language-toggle]").forEach((button) => {
      button.textContent = language === "zh-CN" ? "English" : "中文";
      button.setAttribute(
        "aria-label",
        language === "zh-CN" ? "Switch to English" : "切换为中文",
      );
    });
    listeners.forEach((listener) => listener(language));
  }

  function setLanguage(nextLanguage) {
    if (!dictionaries[nextLanguage]) return;
    if (root.WebI18n) {
      root.WebI18n.setLanguage(nextLanguage);
      return;
    }
    language = nextLanguage;
    try {
      root.localStorage.setItem(storageKey, language);
    } catch (_) {
      /* Storage may be disabled. */
    }
    apply();
  }

  language =
    root.WebI18n?.getLanguage() ||
    savedLanguage() ||
    (/^zh\b/i.test(root.navigator?.language || "") ? "zh-CN" : "en");
  if (!dictionaries[language]) language = "zh-CN";
  root.SiteI18n = {
    setBrand(value) {
      brand = value;
      apply();
    },
    t,
    getLanguage: () => language,
    setLanguage,
    onChange: (listener) => listeners.add(listener),
  };
  if (root.WebI18n)
    root.WebI18n.onChange((next) => {
      language = next;
      apply();
    });
  if (root.document) {
    root.document
      .querySelectorAll("[data-site-language-toggle]")
      .forEach((button) => {
        if (!root.WebI18n)
          button.addEventListener("click", () =>
            setLanguage(language === "zh-CN" ? "en" : "zh-CN"),
          );
      });
    apply();
  }
})(typeof window === "undefined" ? globalThis : window);
