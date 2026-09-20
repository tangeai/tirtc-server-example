const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const staticDir = path.join(__dirname, '..', 'static');
const source = fs.readFileSync(path.join(staticDir, 'site-i18n.js'), 'utf8');

function loadSiteI18n(savedLanguage = null) {
  const storage = new Map();
  if (savedLanguage) storage.set('xiaotai.site.language', savedLanguage);
  const text = { dataset: { i18n: 'site.boards' }, textContent: '开发板' };
  const aria = {
    dataset: { i18nAriaLabel: 'site.mainNav' },
    value: '主导航',
    getAttribute() { return this.value; },
    setAttribute(_name, value) { this.value = value; },
  };
  const toggle = {
    textContent: 'English',
    value: '',
    addEventListener(_type, listener) { this.click = listener; },
    setAttribute(_name, value) { this.value = value; },
  };
  const elements = {
    '[data-i18n]': [text],
    '[data-i18n-aria-label]': [aria],
    '[data-site-language-toggle]': [toggle],
  };
  const window = {
    navigator: { language: 'zh-CN' },
    localStorage: {
      getItem(key) { return storage.get(key) || null; },
      setItem(key, value) { storage.set(key, value); },
    },
    document: {
      documentElement: { lang: 'zh-CN' },
      querySelectorAll(selector) { return elements[selector] || []; },
    },
  };
  vm.runInNewContext(source, { window });
  return { window, text, aria, toggle, storage };
}

test('switching language updates UI, language metadata, and persists across pages', () => {
  const { window, text, aria, toggle, storage } = loadSiteI18n();
  assert.equal(window.SiteI18n.getLanguage(), 'zh-CN');
  toggle.click();
  assert.equal(window.document.documentElement.lang, 'en');
  assert.equal(text.textContent, 'Boards');
  assert.equal(aria.value, 'Main navigation');
  assert.equal(toggle.textContent, '中文');
  assert.equal(storage.get('xiaotai.site.language'), 'en');
  assert.equal(loadSiteI18n('en').text.textContent, 'Boards');
  toggle.click();
  assert.equal(text.textContent, '开发板');
  assert.equal(aria.value, '主导航');
});

test('every marked home and boards UI string has an English translation', () => {
  const { window } = loadSiteI18n('en');
  for (const file of ['index.html', 'boards.html']) {
    const html = fs.readFileSync(path.join(staticDir, file), 'utf8');
    for (const [, key] of html.matchAll(/data-i18n(?:-aria-label|-content|-alt)?="([^"]+)"/g)) {
      assert.notEqual(window.SiteI18n.t(key), key, `${file}: ${key}`);
    }
  }
  for (const key of ['site.menuOpen', 'site.menuClose', 'home.copy', 'home.copied', 'home.copyCommand', 'home.commandCopied', 'boards.ready', 'boards.adapting', 'boards.planned', 'boards.details', 'boards.productImage', 'boards.count']) {
    assert.notEqual(window.SiteI18n.t(key), key, key);
  }
  assert.equal(window.SiteI18n.t('boards.productImage', { name: '原始板卡名' }), '原始板卡名 product image');
});
