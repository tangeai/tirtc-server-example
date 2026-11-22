const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const ts=require('typescript');
const code=ts.transpileModule(fs.readFileSync(path.join(__dirname,'../src/lib/api.ts'),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
function fixture(){
 const values=new Map([['token','old-session']]);let resolveFetch,resolveJSON;const redirects=[];
 const fetchPromise=new Promise(r=>{resolveFetch=r}),jsonPromise=new Promise(r=>{resolveJSON=r});
 const module={exports:{}};
 vm.runInNewContext(code,{module,exports:module.exports,window:{},AbortSignal,DOMException,
 localStorage:{getItem:k=>values.get(k)||null,removeItem:k=>values.delete(k)},sessionStorage:{setItem(){},removeItem(){}},
 location:{replace:p=>redirects.push(p)},fetch:()=>fetchPromise});
 return {api:module.exports,values,redirects,respond(status,data,delayedJSON=false){resolveFetch({status,ok:status===200,json:()=>delayedJSON?jsonPromise:Promise.resolve({code:status,data})});},json:resolveJSON};
}
for(const status of [200,401])test(`old ${status} cannot affect a replacement session`,async()=>{
 const f=fixture(),pending=f.api.request('/v1/user/device/list');f.values.set('token','new-session');f.respond(status,['old-device']);
 await assert.rejects(pending,e=>e.code===-2);assert.equal(f.values.get('token'),'new-session');assert.deepEqual(f.redirects,[]);
});
test('account change while decoding JSON rejects old data',async()=>{
 const f=fixture(),pending=f.api.request('/v1/user/device/list');f.respond(200,null,true);await Promise.resolve();f.values.set('token','new-session');f.json({code:200,data:['old-device']});
 await assert.rejects(pending,e=>e.code===-2);assert.equal(f.values.get('token'),'new-session');
});
test('current-session 401 still logs out',async()=>{
 const f=fixture(),pending=f.api.request('/v1/user/device/list');f.respond(401,null);await assert.rejects(pending,e=>e.code===401);assert.equal(f.values.has('token'),false);assert.deepEqual(f.redirects,['/login']);
});
test('anonymous response is independent of account changes',async()=>{
 const f=fixture(),pending=f.api.request('/v1/config/navigation',{anonymous:true});f.values.set('token','new-session');f.respond(200,'public');assert.equal(await pending,'public');
});
