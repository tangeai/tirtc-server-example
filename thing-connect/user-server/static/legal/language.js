// Legal documents follow the same effective language and switch policy as the site.
(function () {
  function synchronize() {
    const match = location.pathname.match(/\/(terms|privacy)(?:\.en)?\.html$/);
    if (!match || !window.WebI18n) return;
    const filename = match[1] + (WebI18n.getLanguage() === 'en' ? '.en' : '') + '.html';
    if (!location.pathname.endsWith('/' + filename)) {
      location.replace(new URL(filename + location.search + location.hash, location.href).href);
    }
  }
  synchronize();
  window.WebI18n?.onChange(synchronize);
})();
