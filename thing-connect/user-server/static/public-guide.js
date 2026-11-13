// Progressive enhancement: all content and navigation work without JavaScript.
(() => {
  const i18n = window.WebI18n;
  if (i18n) {
    // This template contains system documentation. Explicitly exclude published
    // catalog values, branding and commands from text bindings.
    const scope = document.querySelector('[data-guide-copy]');
    const walker = document.createTreeWalker(scope, NodeFilter.SHOW_TEXT);
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    for (const node of nodes) {
      if (node.parentElement.closest('script, style, pre, code, [data-user-content], [data-brand-zh], [data-language-toggle]')) continue;
      const original = node.textContent;
      const key = original.trim();
      if (!key) continue;
      // Link arrows are decoration, not part of article source labels.
      const suffix = key.endsWith(' →') ? ' →' : '';
      const source = suffix ? key.slice(0, -suffix.length) : key;
      i18n.bindText(node, () => original.replace(key, i18n.t(source) + suffix));
    }
    const attributes = [...scope.querySelectorAll('[aria-label], img[alt]')].map(element => ({
      element, values: ['aria-label', 'alt'].filter(name => element.hasAttribute(name)).map(name => [name, element.getAttribute(name)])
    }));
    const title = document.title;
    function updateGuideLanguage() {
      scope.querySelectorAll('[data-brand-zh]').forEach(element => {
        element.textContent = i18n.getLanguage() === 'en' ? element.dataset.brandEn || element.dataset.brandZh : element.dataset.brandZh;
      });
      attributes.forEach(({element, values}) => values.forEach(([name, value]) => {
        if (!element.closest('[data-user-content]')) element.setAttribute(name, i18n.t(value));
      }));
      const heading = scope.querySelector('h1')?.textContent;
      const brand = scope.querySelector('[data-brand-zh]')?.textContent;
      document.title = i18n.getLanguage() === 'en' && heading && brand ? heading + ' | ' + brand : title;
    }
    updateGuideLanguage();
    i18n.onChange(updateGuideLanguage);
  }
  const menu = document.querySelector('.guide-mobile');
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && menu?.open) {
      menu.open = false;
      menu.querySelector('summary').focus();
    }
  });
  document.addEventListener('click', (event) => {
    if (menu?.open && (!menu.contains(event.target) || event.target.closest('a'))) menu.open = false;
    const questionLink = event.target.closest('.faq-sidebar a[href^="#"]');
    if (questionLink) openAnswer(questionLink.getAttribute('href'));
  });
  function openAnswer(hash = location.hash) {
    let id;
    try { id = decodeURIComponent(hash.slice(1)); } catch { return; }
    const answer = document.getElementById(id);
    if (answer?.matches('details.faq-item')) answer.open = true;
  }
  window.addEventListener('hashchange', () => openAnswer());
  openAnswer();
})();
