// Run from admin/admin-web: node prototypes/build-ai-recognition-prototype.mjs
import { build } from 'esbuild';
import { writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
const result=await build({absWorkingDir:root,entryPoints:['prototypes/ai-recognition.prototype.tsx'],bundle:true,write:false,outdir:'prototype-build',minify:true,jsx:'automatic',define:{'process.env.NODE_ENV':'"production"'}});
const js=result.outputFiles.find(f=>f.path.endsWith('.js')).text.replace(/<\/script/gi,'<\\/script');
const css=result.outputFiles.find(f=>f.path.endsWith('.css'))?.text||'';
const html=`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="icon" href="data:,"><title>AI 识别原型 · ThingConnect 管理后台</title><style>${css}</style></head><body><div id="root"></div><script>${js}</script></body></html>`.replace(/[ \t]+$/gm,'').replace(/^[ \t]+/gm,indent=>indent.replace(/\t/g,'  '));
await writeFile(new URL('./ai-recognition.prototype.html',import.meta.url),html);
await writeFile(new URL('./ai-recognition-user.prototype.html',import.meta.url),html.replace('<div id="root"></div>','<div id="root" data-view="user"></div>').replace('AI 识别原型 · ThingConnect 管理后台','我的设备 · AI 识别配置原型'));
console.log('Built standalone prototype with existing Admin components and styles.');
