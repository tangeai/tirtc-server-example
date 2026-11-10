(() => {
  "use strict";
  const i18n = window.SiteI18n;
  const t = (key, values) => i18n.t(key, values);
  const nav = document.querySelector(".site-header .nav");
  const menu = document.querySelector(".menu-button");
  function closeMenu() {
    nav.classList.remove("menu-open");
    menu.setAttribute("aria-expanded", "false");
    menu.setAttribute("aria-label", t("site.menuOpen"));
  }
  menu.addEventListener("click", () => {
    const open = nav.classList.toggle("menu-open");
    menu.setAttribute("aria-expanded", String(open));
    menu.setAttribute(
      "aria-label",
      t(open ? "site.menuClose" : "site.menuOpen"),
    );
  });
  nav
    .querySelectorAll("a")
    .forEach((link) => link.addEventListener("click", closeMenu));
  document.addEventListener("click", (event) => {
    if (!nav.contains(event.target)) closeMenu();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && nav.classList.contains("menu-open")) {
      closeMenu();
      menu.focus();
    }
  });
  let loggedIn = false;
  try {
    loggedIn = Boolean(localStorage.getItem("token"));
  } catch {
    /* Storage can be disabled. */
  }
  document.querySelectorAll("[data-auth-guest]").forEach((item) => {
    item.hidden = loggedIn;
  });
  document.querySelectorAll("[data-auth-user]").forEach((item) => {
    item.hidden = !loggedIn;
  });
  document.querySelector("#year").textContent = String(
    new Date().getFullYear(),
  );
  document.querySelectorAll("[data-browse]").forEach((button) =>
    button.addEventListener("click", () => {
      document.querySelector("#boards").scrollIntoView({
        behavior: matchMedia("(prefers-reduced-motion: reduce)").matches
          ? "instant"
          : "smooth",
      });
    }),
  );

  // The homepage consumes the existing catalog in its published sort order.
  // Links go directly to each board's resources, with no second selection step.
  const grid = document.querySelector("#home-boards");
  const status = document.querySelector("#home-board-state");
  const retry = document.querySelector("#home-board-retry");
  let boards = [];
  let catalogState = "loading";
  let loading = false;
  const element = (tag, className, text) => {
    const node = document.createElement(tag);
    node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };
  function resourceURL(value, image = false) {
    if (typeof value !== "string" || !value) return "";
    if (
      image &&
      /^\/v1\/board-images\/[a-f0-9]{64}\.(?:png|jpg|webp)$/.test(value)
    )
      return value;
    try {
      const url = new URL(value);
      return url.protocol === "https:" && !url.username && !url.password
        ? url.href
        : "";
    } catch {
      return "";
    }
  }
  function boardCard(board) {
    const card = element("article", "catalog-card");
    const art = element("div", "product-art");
    const fallback = element(
      "span",
      "board-image-fallback",
      board.chip || board.model || "BOARD",
    );
    art.append(fallback);
    const imageURL = resourceURL(board.image_url, true);
    if (imageURL) {
      const img = element("img", "");
      img.alt = t("boards.productImage", { name: board.name });
      img.loading = "lazy";
      img.addEventListener("load", () => {
        fallback.hidden = true;
      });
      img.addEventListener(
        "error",
        () => {
          img.remove();
          fallback.hidden = false;
        },
        { once: true },
      );
      img.src = imageURL;
      art.append(img);
    }
    const info = element("div", "catalog-info");
    info.append(
      element(
        "small",
        "",
        [board.vendor, board.chip].filter(Boolean).join(" · "),
      ),
      element("h3", "", board.name),
    );
    const statusKey = {
      ready: "boards.ready",
      adapting: "boards.adapting",
      planned: "boards.planned",
    }[board.adaptation_status];
    if (statusKey) info.append(element("span", "board-status", t(statusKey)));
    const capabilities = Array.isArray(board.capabilities)
      ? board.capabilities.filter((value) => typeof value === "string")
      : [];
    if (capabilities.length)
      info.append(element("p", "board-capabilities", capabilities.join(" · ")));
    const actions = element("div", "catalog-actions");
    for (const [field, label, className] of [
      ["firmware_url", "landing.firmware", "primary"],
      ["flashing_guide_url", "landing.flashGuide", "guide-link"],
    ]) {
      const url = resourceURL(board[field]);
      if (!url) continue;
      const link = element("a", className, t(label));
      link.href = url;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      actions.append(link);
    }
    if (!actions.childElementCount)
      actions.append(
        element("span", "board-pending", t("landing.pendingLinks")),
      );
    info.append(actions);
    card.append(art, info);
    return card;
  }
  function renderCatalog() {
    grid.replaceChildren(...boards.map(boardCard));
    grid.setAttribute("aria-busy", String(catalogState === "loading"));
    status.hidden = catalogState === "ready";
    const key = {
      loading: "landing.loading",
      empty: "landing.empty",
      error: "landing.failed",
    }[catalogState];
    // This element has a changing message; do not let static i18n restore its initial text.
    if (key) status.textContent = t(key);
    retry.hidden = catalogState !== "error";
  }
  status.removeAttribute("data-i18n");
  async function loadCatalog() {
    if (loading) return;
    loading = true;
    catalogState = "loading";
    renderCatalog();
    try {
      const response = await fetch("/v1/boards", {
        headers: { Accept: "application/json" },
        cache: "no-store",
        signal: AbortSignal.timeout(8000),
      });
      const body = await response.json();
      if (
        !response.ok ||
        body.code !== 200 ||
        !Array.isArray(body.data?.boards)
      )
        throw new Error("Invalid catalog");
      boards = body.data.boards
        .filter((board) => board && typeof board.name === "string")
        .slice(0, 3);
      catalogState = boards.length ? "ready" : "empty";
    } catch {
      boards = [];
      catalogState = "error";
    } finally {
      loading = false;
      renderCatalog();
    }
  }
  retry.addEventListener("click", loadCatalog);

  const simulator = document.querySelector("#simulator-details");
  const copyStatus = document.querySelector(".copy-status");
  document.querySelectorAll("[data-open-simulator]").forEach((link) =>
    link.addEventListener("click", () => {
      simulator.open = true;
    }),
  );
  function openSimulatorHash() {
    if (location.hash === "#simulator") simulator.open = true;
  }
  openSimulatorHash();
  window.addEventListener("hashchange", openSimulatorHash);
  const tabs = [...document.querySelectorAll("[data-sim-tab]")];
  function selectPlatform(selected) {
    tabs.forEach((tab) => {
      const active = tab === selected;
      tab.setAttribute("aria-selected", String(active));
      tab.tabIndex = active ? 0 : -1;
      document.getElementById(tab.getAttribute("aria-controls")).hidden =
        !active;
    });
    copyStatus.textContent = "";
  }
  tabs.forEach((tab, index) => {
    tab.tabIndex = index ? -1 : 0;
    tab.addEventListener("click", () => selectPlatform(tab));
    tab.addEventListener("keydown", (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key))
        return;
      event.preventDefault();
      const next =
        event.key === "Home"
          ? 0
          : event.key === "End"
            ? tabs.length - 1
            : (index + (event.key === "ArrowLeft" ? -1 : 1) + tabs.length) %
              tabs.length;
      selectPlatform(tabs[next]);
      tabs[next].focus();
    });
  });
  document.querySelectorAll("[data-copy]").forEach((button) =>
    button.addEventListener("click", async () => {
      const code = document.getElementById(button.dataset.copy);
      try {
        await navigator.clipboard.writeText(code.textContent);
        copyStatus.textContent = t("landing.copied");
      } catch {
        const selection = window.getSelection();
        const range = document.createRange();
        range.selectNodeContents(code);
        selection.removeAllRanges();
        selection.addRange(range);
        copyStatus.textContent = t("landing.copyFallback");
      }
    }),
  );
  i18n.onChange(() => {
    menu.setAttribute(
      "aria-label",
      t(
        nav.classList.contains("menu-open")
          ? "site.menuClose"
          : "site.menuOpen",
      ),
    );
    copyStatus.textContent = "";
    renderCatalog();
  });
  const initial = document.getElementById("public-catalog-data");
  if (initial) {
    try {
      const value = JSON.parse(initial.textContent);
      if (!Array.isArray(value.boards)) throw new Error("Invalid catalog");
      boards = value.boards.slice(0, 3);
      catalogState = value.status;
      renderCatalog();
      return;
    } catch {
      /* Fetch fallback for invalid bootstrap data. */
    }
  }
  loadCatalog();
})();
