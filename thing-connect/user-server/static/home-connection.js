(() => {
  "use strict";
  const host = document.querySelector(".showcase"),
    screen = host.querySelector(".screen");
  const svg = host.querySelector(".connection-layer"),
    label = host.querySelector(".connection-label");
  const t = (key) => window.SiteI18n.t("landing." + key);
  function render(geometryOnly = false) {
    const scene = Number(screen.dataset.scene || 1),
      connected = screen.classList.contains("call-connected");
    const paused =
      screen.classList.contains("animation-paused") || document.hidden;
    const a = host
        .querySelector('[data-feature="' + scene + '"]')
        .getBoundingClientRect(),
      b = screen.getBoundingClientRect(),
      h = host.getBoundingClientRect();
    const compact = innerWidth <= 1250,
      right = compact && a.x + a.width / 2 > h.x + h.width / 2;
    const x1 = compact
        ? right
          ? a.right - h.x + 4
          : a.left - h.x - 4
        : a.right - h.x + 5,
      y1 = a.y + a.height / 2 - h.y;
    const x2 = right ? b.right - h.x + 16 : b.x - h.x - 16,
      y2 = b.y + b.height / 2 - h.y;
    const mid = (x1 + x2) / 2,
      direction = Math.sign(y2 - y1) || 1;
    // Separate outbound and inbound lanes, including the turns and endpoints.
    const route = (offset) => {
      if (compact) {
        const bend = right ? h.width + 8 - offset : -8 + offset;
        return `M${x1},${y1 + offset} H${bend} V${y2 - offset} H${x2}`;
      }
      const bend = mid - offset * direction;
      return `M${x1},${y1 + offset} H${bend} V${y2 + offset} H${x2}`;
    };
    const routes = [route(-6), route(6)];
    const color = "#087d89";
    host.style.setProperty("--wire", color);
    const status = t(
      scene === 1
        ? connected
          ? "connectionDuplex"
          : "connectionPending"
        : ["connectionAI", "", "connectionCall", "connectionLive"][scene],
    );
    label.textContent = status;
    label.style.left =
      (compact ? (right ? h.width - 76 : 76) : (x1 + x2) / 2) + "px";
    label.style.top =
      (compact
        ? host.querySelector(".feature-tabs").getBoundingClientRect().bottom -
          h.y +
          22
        : Math.min(y1, y2) - 22) + "px";
    if (geometryOnly === true && svg.querySelector("#flow-route-0")) {
      routes.forEach((d, i) =>
        svg.querySelector("#flow-route-" + i).setAttribute("d", d),
      );
      const nodes = svg.querySelectorAll(".connection-node");
      [-6, 6].forEach((offset, i) => {
        nodes[i * 2].setAttribute("cx", x1);
        nodes[i * 2].setAttribute("cy", y1 + offset);
        nodes[i * 2 + 1].setAttribute("cx", x2);
        nodes[i * 2 + 1].setAttribute("cy", y2 + (compact ? -offset : offset));
      });
      return;
    }
    const duplex = scene !== 1 || connected;
    let drawing = routes
      .map(
        (d, i) =>
          `<path id="flow-route-${i}" class="connection-track" d="${d}" style="opacity:${i && !duplex ? 0.12 : 0.45}"/>`,
      )
      .join("");
    const flow = (reverse, index) => {
      const motion = `<animateMotion dur="${scene === 0 ? "4.8" : "4.2"}s" begin="-${index * 1.4}s" repeatCount="indefinite" keyPoints="${reverse ? "1;0" : "0;1"}" keyTimes="0;1" calcMode="linear" rotate="auto"><mpath href="#flow-route-${reverse ? 1 : 0}"/></animateMotion>`;
      return `<rect x="-5" y="-2" width="10" height="4" rx="1" class="connection-particle ${reverse ? "connection-return" : ""}">${motion}</rect>`;
    };
    for (let n = 0; n < 3; n++) {
      drawing += flow(false, n);
      if (duplex) drawing += flow(true, n + 0.5);
    }
    for (const offset of [-6, 6])
      drawing += `<circle class="connection-node" cx="${x1}" cy="${y1 + offset}" r="2.5"/><circle class="connection-node" cx="${x2}" cy="${y2 + (compact ? -offset : offset)}" r="3"/>`;
    const time = svg.getCurrentTime();
    svg.innerHTML = drawing;
    svg.setAttribute("viewBox", `0 0 ${h.width} ${h.height}`);
    svg.setCurrentTime(time || 1.1);
    if (paused) svg.pauseAnimations();
    else svg.unpauseAnimations();
  }
  const board = host.querySelector(".board"),
    hover = matchMedia("(hover:hover) and (pointer:fine)");
  let restBounds = null,
    frame = 0;
  function setFacing(facing) {
    if (board.classList.contains("is-facing") === facing) return;
    if (facing) restBounds = board.getBoundingClientRect();
    board.classList.toggle("is-facing", facing);
    host.classList.toggle("board-facing", facing);
    cancelAnimationFrame(frame);
    const until = performance.now() + 600;
    function follow() {
      render(true);
      if (performance.now() < until) frame = requestAnimationFrame(follow);
    }
    if (!document.hidden) frame = requestAnimationFrame(follow);
  }
  board.addEventListener("pointerenter", () => {
    if (hover.matches) setFacing(true);
  });
  // Retain the resting hit area while rotating so edge hovering never flickers.
  document.addEventListener("pointermove", (event) => {
    if (!board.classList.contains("is-facing")) return;
    const inside = (rect) =>
      event.clientX >= rect.left &&
      event.clientX <= rect.right &&
      event.clientY >= rect.top &&
      event.clientY <= rect.bottom;
    if (
      !hover.matches ||
      (!inside(restBounds) && !inside(board.getBoundingClientRect()))
    )
      setFacing(false);
  });
  document.documentElement.addEventListener("pointerleave", () =>
    setFacing(false),
  );
  window.addEventListener("blur", () => setFacing(false));
  window.addEventListener("resize", () => setFacing(false));
  window.addEventListener("pagehide", () => cancelAnimationFrame(frame));
  window.SiteI18n.onChange(() => render());
  new MutationObserver(render).observe(screen, {
    attributes: true,
    attributeFilter: ["class", "data-scene"],
  });
  new ResizeObserver(render).observe(host);
  document.addEventListener("visibilitychange", () => {
    if (document.hidden) cancelAnimationFrame(frame);
    render();
  });
  window.addEventListener("pageshow", () => render());
  render();
})();
