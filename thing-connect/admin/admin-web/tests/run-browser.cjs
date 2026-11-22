const fs=require('node:fs'),path=require('node:path'),{spawnSync}=require('node:child_process');
const output=path.resolve(__dirname,'../../../../output/playwright/admin-browser');
fs.mkdirSync(output,{recursive:true});
const results=[];
for(const suite of fs.readdirSync(__dirname).filter(n=>n.endsWith('.browser.cjs')).sort()){
 const start=Date.now();
 const run=spawnSync(process.execPath,[path.join(__dirname,suite)],{encoding:'utf8',timeout:240000,maxBuffer:5*1024*1024});
 fs.writeFileSync(path.join(output,suite+'.log'),(run.stdout||'')+(run.stderr||'')+(run.error?'\n'+run.error.message:''));
 const result={suite,status:run.status===0?'pass':'fail',durationMs:Date.now()-start};results.push(result);
 console.log(result.status.toUpperCase(),suite);
 fs.writeFileSync(path.join(output,'results.json'),JSON.stringify({at:new Date().toISOString(),mode:'synthetic-api-real-chromium',results},null,2));
}
if(results.some(r=>r.status!=='pass'))process.exitCode=1;
