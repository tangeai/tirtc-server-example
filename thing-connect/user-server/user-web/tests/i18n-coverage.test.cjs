const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript');
const root = path.resolve(__dirname, '../../..');
const messages = Object.assign({}, ...['internal/webui/common.json', 'user-server/static/user-i18n.json', 'ai-server/static/ai-i18n.json', 'call-server/handler/call-i18n.json'].map(file => JSON.parse(fs.readFileSync(path.join(root, file), 'utf8'))));
const han = /[\u3400-\u9fff]/;
const languageNames = new Set(['简体中文', '语言 / Language']);
function files(dir) { return fs.readdirSync(dir, {withFileTypes:true}).flatMap(entry => entry.isDirectory() ? files(path.join(dir,entry.name)) : [path.join(dir,entry.name)]); }
test('Chinese UI literals, including conditional labels, have English translations', () => {
 const missing=[];
 for(const file of files(path.join(root,'user-server/user-web/src')).filter(file=>/\.tsx?$/.test(file)&&!file.endsWith('.test.ts'))) {
  const source=ts.createSourceFile(file,fs.readFileSync(file,'utf8'),ts.ScriptTarget.Latest,true,ts.ScriptKind.TSX);
  function visit(node) {
   if((ts.isStringLiteralLike(node)||ts.isJsxText(node)) && han.test(node.text)) {
    const text=ts.isJsxText(node) ? node.text.trim() : node.text;
    if(!(file.endsWith('LanguageSwitcher.tsx')&&languageNames.has(text)) && !messages[text])missing.push(path.relative(root,file)+': '+text);
   }
   ts.forEachChild(node,visit);
  }
  visit(source);
 }
 assert.deepEqual(missing,[]);
});
test('English legal documents preserve section anchors and use the site language bootstrap', () => {
 for(const name of ['terms','privacy']) {
  const chinese=fs.readFileSync(path.join(root,'user-server/static/legal',name+'.html'),'utf8');
  const english=fs.readFileSync(path.join(root,'user-server/static/legal',name+'.en.html'),'utf8');
  assert.deepEqual([...english.matchAll(/<h2 id="([^"]+)"/g)].map(x=>x[1]),[...chinese.matchAll(/<h2 id="([^"]+)"/g)].map(x=>x[1]));
  assert(!han.test(english));
  for(const doc of [chinese,english]){assert(doc.includes('/v1/config/language.js'));assert(doc.includes('src="language.js"'));}
 }
});

test('all built-in guide and FAQ content has an English translation', () => {
 const vm=require('node:vm');let guideMessages;
 vm.runInNewContext(fs.readFileSync(path.join(root,'user-server/static/public-guide-i18n.js'),'utf8'),{window:{WebI18n:{addMessages:value=>{guideMessages=value;}}}});
 const missing=[];
 for(const file of ['public_content.go','public_tutorials.go']) {
  const source=fs.readFileSync(path.join(root,'user-server/handler',file),'utf8');
  for(const match of source.matchAll(/"(?:[^"\\]|\\.)*"/g)) {
   const text=JSON.parse(match[0]);
   if(han.test(text)&&(!guideMessages[text]||han.test(guideMessages[text])))missing.push(text);
  }
 }
 assert.deepEqual(missing,[]);
});
