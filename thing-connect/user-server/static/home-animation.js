(() => {
  "use strict";
  const t = (key) => window.SiteI18n.t("landing." + key);
  const screen = document.querySelector(".showcase .screen");
  const visual = document.querySelector("#screen-visual");
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  let callTimer;
  let currentFeature = 1;
  const bars =
    '<div class="demo-bars">' +
    Array.from({ length: 9 }, (_, i) => '<i style="--bar:' + i + '"></i>').join(
      "",
    ) +
    "</div>";
  const screenScenes = [
    '<div class="ai-face"><b></b><b></b><i></i></div><div class="chat-line question"><span data-scene-text="question"></span></div><div class="chat-line answer"><span data-scene-text="answer"></span></div>' +
      bars,
    '<div class="call-visual"><div class="call-ripple"></div><div class="call-avatar"><svg viewBox="0 0 40 40"><path d="M12 8c-5 3-3 12 4 19s14 8 17 3l-6-7-5 3c-4-2-7-5-9-9l3-4z" fill="currentColor"/></svg></div><span class="call-badge"><span data-scene-text="wechatUser"></span></span></div>' +
      bars,
    '<div class="intercom-visual"><div class="endpoint"><i></i><b><span data-scene-text="deviceA"></span></b></div><div class="voice-path"><i></i><i></i><i></i><span>⇄</span></div><div class="endpoint"><i></i><b><span data-scene-text="deviceB"></span></b></div></div><div class="intercom-note"><span data-scene-text="group"></span></div>' +
      bars,
    '<div class="live-preview camera-preview"><svg viewBox="0 0 180 92" aria-hidden="true"><defs><linearGradient id="camera-ground" x2="0" y2="1"><stop stop-color="#a4b0ac"/><stop offset="1" stop-color="#c4c9bd"/></linearGradient></defs><rect width="180" height="92" fill="#b9c9c8"/><path d="M0 0h180v20H0z" fill="#9fb7b8"/><path d="M0 19h180v29H0z" fill="#d0cec0"/><path d="M0 48h180v44H0z" fill="url(#camera-ground)"/><path d="M0 48h180M45 48 12 92M90 48v44M135 48l33 44M0 68h180" stroke="#919c92" stroke-width=".6"/><path d="M11 15h46v39H11zM126 15h43v39h-43z" fill="#a9b2a6"/><path d="M10 13h48v5H10zM124 13h46v5h-46z" fill="#64766c"/><rect x="57" y="20" width="69" height="30" fill="#485f59"/><path d="M62 21v29m8-29v29m8-29v29m8-29v29m8-29v29m8-29v29m8-29v29m8-29v29" stroke="#9aa99d" stroke-width="2"/><path d="M58 30h67m-67 12h67" stroke="#83958b" stroke-width="1"/><rect x="136" y="25" width="16" height="9" rx="1" fill="#667d76"/><path d="M0 60h29l-5 12H0zM155 58h25v13h-22z" fill="#7e947b"/><g fill="#567b65"><circle cx="9" cy="40" r="14"/><circle cx="21" cy="44" r="9"/><circle cx="176" cy="37" r="13"/><circle cx="163" cy="44" r="10"/></g><path d="M0 83h180v9H0z" fill="#17312f33"/></svg><span class="camera-channel"><span data-scene-text="channel"></span></span><span class="camera-live"><span data-scene-text="liveBadge"></span></span><span class="camera-time"><span data-scene-text="sampleTime"></span></span><div class="preview-audio">▂ ▄ ▆ ▃ ▅</div></div>',
  ];
  const sceneTitles = ["aiAnswer", "calling", "intercomStatus", "preview"];
  const sceneCopies = ["aiReply", "outgoing", "endpoints", "previewCopy"];
  function updateAnimationState() {
    screen.classList.toggle(
      "animation-paused",
      reducedMotion.matches || document.hidden,
    );
  }
  function feature(i) {
    clearTimeout(callTimer);
    currentFeature = i;
    document.querySelectorAll("[data-feature]").forEach((b) => {
      const active = +b.dataset.feature === i;
      b.classList.toggle("active", active);
      b.setAttribute("aria-pressed", String(active));
      b.setAttribute("aria-controls", "screen-visual");
    });
    screen.dataset.scene = String(i);
    screen.classList.remove("call-connected");
    visual.innerHTML = screenScenes[i];
    visual.querySelectorAll("[data-scene-text]").forEach((node) => {
      node.textContent = t(node.dataset.sceneText);
    });
    document.querySelector("#screen-title").textContent = t(sceneTitles[i]);
    document.querySelector("#screen-copy").textContent = t(sceneCopies[i]);
    updateAnimationState();
    if (i === 1) {
      const connect = () => {
        screen.classList.add("call-connected");
        document.querySelector("#screen-title").textContent = t("connected");
        document.querySelector("#screen-copy").textContent = t("twoWay");
      };
      if (reducedMotion.matches) connect();
      else callTimer = setTimeout(connect, 1800);
    }
  }
  document
    .querySelectorAll("[data-feature]")
    .forEach((b) => (b.onclick = () => feature(+b.dataset.feature)));
  reducedMotion.addEventListener("change", () => {
    feature(currentFeature);
  });
  window.SiteI18n.onChange(() => feature(currentFeature));
  document.addEventListener("visibilitychange", () => {
    clearTimeout(callTimer);
    if (document.hidden) updateAnimationState();
    else feature(currentFeature);
  });
  window.addEventListener("pagehide", () => clearTimeout(callTimer));
  feature(1);
})();
