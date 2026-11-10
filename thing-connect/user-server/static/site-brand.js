// Load public site naming once per page; keep the built-in brand on failure.
(() => {
  "use strict";
  const i18n = window.SiteI18n;
  function updateTitle() {
    const title = document.querySelector("title");
    const key =
      title?.dataset.i18n ||
      (title?.dataset.sitePage === "index.html"
        ? "home.title"
        : "boards.title");
    if (key === "home.title" || key === "boards.title") {
      const prefix =
        key === "home.title"
          ? i18n.t("landing.heroKicker")
          : i18n.t("site.boards");
      document.title = `${prefix} | ${i18n.t("site.brand")}`;
    }
    document.querySelectorAll(".brand[aria-label]").forEach((element) => {
      element.setAttribute("aria-label", i18n.t("site.brand"));
    });
  }
  i18n.onChange(updateTitle);
  const initial = document.getElementById("site-brand-data");
  if (initial) {
    try {
      i18n.setBrand(JSON.parse(initial.textContent));
      return;
    } catch {
      /* Fetch fallback. */
    }
  }
  fetch("/v1/config/site", {
    cache: "no-store",
    signal: AbortSignal.timeout(5000),
  })
    .then(async (response) => {
      if (!response.ok) return;
      const body = await response.json();
      const value = body.data;
      if (
        body.code !== 200 ||
        typeof value?.name !== "string" ||
        !value.name.trim() ||
        typeof value.name_en !== "string"
      )
        return;
      i18n.setBrand({ name: value.name, name_en: value.name_en });
    })
    .catch(() => {
      /* Optional naming uses the built-in default if unavailable. */
    });
})();
