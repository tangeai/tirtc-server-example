const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const html = fs.readFileSync(path.join(__dirname,'../static/auth.html'),'utf8');
const source = html.slice(html.indexOf('function legalURL('),html.indexOf('const legalDocuments'));
test('legal links accept local documents and HTTPS, reject empty or unsafe destinations',()=>{
  let value='';
  const ctx={URL,location:{origin:'https://console.example'},document:{querySelector:()=>({content:value})}};
  vm.createContext(ctx);vm.runInContext(source,ctx);
  for(const candidate of ['', ' ', 'javascript:alert(1)', 'http://external.example/legal', 'https://user:pass@example.com/legal']){
    value=candidate;assert.equal(ctx.legalURL('policy'),'');
  }
  value='/static/legal/terms.html';assert.equal(ctx.legalURL('policy'),'https://console.example/static/legal/terms.html');
  value='https://external.example/legal';assert.equal(ctx.legalURL('policy'),value);
});
test('both configured legal documents exist and can be read without scripts',()=>{
  const urls=[...html.matchAll(/name="auth-(?:user-agreement|privacy-policy)" content="([^"]+)"/g)].map(m=>m[1]);
  assert.equal(urls.length,2);
  for(const url of urls){
    assert.match(url,/^\/static\/legal\/.+\.html$/);
    const document=fs.readFileSync(path.join(__dirname,'..',url),'utf8');
    assert.match(document,/<h1>小钛控制台/);assert.match(document,/dpo@tange.ai/);
    assert.doesNotMatch(document,/<script/);assert.match(document,/试行稿/);
  }
});
