const test = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
function setup(response, search = '') {
  const redirects = [], removed = [], notices = [];
  const context = { URL, URLSearchParams, Request, Headers, Error,
    location: {search,href:'https://example.com/devices',origin:'https://example.com',replace:url=>redirects.push(url)},
    localStorage:{removeItem:key=>removed.push(key)},sessionStorage:{setItem:(...v)=>notices.push(v)},
    document:{addEventListener(){}},fetch:async()=>{if(response instanceof Error)throw response;return response.clone();}
  };
  context.window=context;vm.runInNewContext(fs.readFileSync(__dirname+'/../static/user-shell.js','utf8'),context);
  return {context,redirects,removed,notices};
}
for(const [status,code] of [[401,401],[200,401]])test(`protected API expiry ${status} redirects once and clears credentials`,async()=>{
  const s=setup(Response.json({code},{status}));
  for(let i=0;i<2;i++)await assert.rejects(s.context.fetch('/v1/user/me',{headers:{Authorization:'Bearer expired'}}),/请重新登录/);
  assert.deepEqual(s.redirects,['/login']);assert.deepEqual(s.removed,['token']);assert.equal(s.notices[0][1],'登录状态已失效，请重新登录');
});
test('public login failures, third-party requests and embedded requests preserve caller handling',async()=>{
  for(const [url,headers,search] of [['/v1/user/login',{},''],['https://other.example/v1/a',{Authorization:'Bearer t'},''],['/v1/user/me',{Authorization:'Bearer t'},'?source=miniprogram']]){
    const s=setup(Response.json({code:401},{status:401}),search);await s.context.fetch(url,{headers});assert.equal(s.redirects.length,0);
  }
});
test('network and server failures never log the user out',async()=>{
  for(const response of [new Error('network'),Response.json({code:500},{status:500})]){
    const s=setup(response);try{await s.context.fetch('/v1/user/me',{headers:{Authorization:'Bearer t'}});}catch{}
    assert.equal(s.redirects.length,0);assert.equal(s.removed.length,0);
  }
});
