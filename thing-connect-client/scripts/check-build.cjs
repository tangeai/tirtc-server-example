const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const target = process.argv[2];
assert.ok(['weapp', 'h5'].includes(target), 'Specify weapp or h5');
const root = path.join(__dirname, '..', 'dist', target);
const read = name => fs.readFileSync(path.join(root, name), 'utf8');
if (target === 'h5') {
  const html = read('index.html');
  assert.match(html, /<script[^>]*type="module"[^>]*src=/, 'H5 must load its application entry');
  assert.doesNotMatch(html, /htmlWebpackPlugin/, 'Template placeholder must be compiled');
} else {
  // Taro emits CSS without validating every WXSS selector. The WeChat compiler
  // rejects universal selectors even though browser previews render them.
  const wxss = read('app-origin.wxss').replace(/\/\*[\s\S]*?\*\//g, '');
  for (const match of wxss.matchAll(/(?:^|[{}])\s*([^{}]+)\{/g)) {
    assert.doesNotMatch(match[1], /(?:^|[\s>+~,])\*(?=$|[\s>+~,.#\[:])/, 'WXSS does not support universal selectors: ' + match[1]);
  }
  const packageBytes = dir => fs.readdirSync(dir, { withFileTypes: true }).reduce((sum, entry) => {
    const file = path.join(dir, entry.name);
    return sum + (entry.isDirectory() ? packageBytes(file) : fs.statSync(file).size);
  }, 0);
  const bytes = packageBytes(root);
  assert.ok(bytes < 1536 * 1024, 'Weapp build exceeds the 1.5 MiB budget; leave headroom for WeChat preview packaging');
  console.log('Weapp build size: ' + Math.ceil(bytes / 1024) + ' KiB (preview packaging still needs WeChat validation)');
  const embeddedLogo = JSON.parse(fs.readFileSync(path.join(__dirname, '../src/generated/auth-logo.json'), 'utf8'));
  assert.ok(read('pages/login/index.js').includes(embeddedLogo), 'Auth logo must be embedded in the login bundle');
  const app = JSON.parse(read('app.json'));
  for (const tab of app.tabBar.list) {
    assert.ok(app.pages.includes(tab.pagePath), 'Tab page must be registered');
    for (const icon of [tab.iconPath, tab.selectedIconPath]) assert.ok(fs.statSync(path.join(root, icon)).size > 0, 'Tab icons must be emitted');
  }
  for (const page of app.pages) {
    assert.ok(read(`${page}.js`).length > 0);
    assert.ok(read(`${page}.wxml`).length > 0);
  }
  assert.equal(JSON.parse(read('native/captcha/index.json')).usingComponents['ne-captcha'], 'plugin://captcha/ne-captcha');
  assert.match(read('base.wxml'), /<auth-captcha\b/, 'Captcha must survive Taro template compilation');
}
console.log(`${target} build artifacts verified`);
