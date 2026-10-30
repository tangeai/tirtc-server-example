// Execute the production language runtime in the existing lightweight page harnesses.
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
module.exports = function languageRuntime(config = {default: 'zh-CN', allow_switch: true}, saved = null, storage = new Map(saved ? [['xiaotai.site.language', saved]] : []), service = "user") {
  const events = {};
  const window = {
    WebLanguageConfig: config, navigator: {language: 'zh-CN'},
    localStorage: {getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), clear: () => storage.clear()},
    document: {documentElement: {}, addEventListener() {}, querySelectorAll: () => []},
    addEventListener: (event, listener) => {events[event] = listener;},
  };
  const root = path.join(__dirname, '../..');
  vm.runInNewContext(fs.readFileSync(path.join(root, 'internal/webui/runtime.js'), 'utf8'), {window});
  for (const file of ['internal/webui/common.json', {user: 'user-server/static/user-i18n.json', ai: 'ai-server/static/ai-i18n.json', call: 'call-server/handler/call-i18n.json'}[service]]) {
    window.WebI18n.addMessages(JSON.parse(fs.readFileSync(path.join(root, file), 'utf8')));
  }
  return window.WebI18n;
};
