const fs=require('node:fs'),path=require('node:path'),{spawnSync}=require('node:child_process');
const root=path.resolve(__dirname,'../../../..'),out=path.join(root,'output/playwright');
fs.mkdirSync(out,{recursive:true});
const coverage={
 'conversations':'会话记录入口、分页、权限凭证、删除确认与失败恢复、迟到响应、空态、移动端与桌面布局',
 'auth-refill':'验证码弹出后密码管理器再次回填旧账号：保持本次凭据、验证不中断、提交账号一致',
 'auth-autofill':'原生自动填充不被渲染覆盖、显示账号与提交一致、验证及请求中换账号隔离、注册/重置密码',
 'captcha-lifecycle':'手机缓存恢复时清理验证遮罩、旧回调隔离、失败重试、带验证码连续三次换账号登录',
 'knowledge-sync':'知识库延迟确认、关联失败复用文件重试、手动刷新、切换后停止旧请求',
 'account-flow':'账号：注册校验、注册成功、退出、验证码、密码重置、登录、ID绑定失败重试',
 'auth-contacts':'人机验证异步初始化与首次点击、同意协议、数字输入、联系人迟到响应、弹窗键盘操作',
 'captcha-layout':'人机验证弹层位置与手机视口',
 'contacts-recognition':'联系人筛选/备注/申请/审批/双向删除提示；识别方案顺序、空选择、版本冲突恢复',
 'cross-page-edge':'HTML 401/502、带空格验证码、知识库草稿/保存期间保护、联系人直接获批',
 'device-prototype':'设备卡片、状态入口、更多菜单、操作按钮、添加设备、中英文与五种宽度',
 'devices-roles':'设备列表无自动轮询、迟到改名响应、系统角色只读、角色草稿/切换/使用中删除',
 'layout':'13个页面 × 320/390/768/1440/1920；中英文溢出、弹窗、空数据、失败状态',
 'player-roles-layout':'实时查看状态与重连工具栏、角色列表紧凑度、多尺寸布局',
 'plugin-many-parameters':'20个输入参数及4个返回参数、增删稳定性、焦点、保存值、桌面/手机布局',
 'resource-editor-edge':'可选字段、旧参数保留、默认值空格、关闭草稿确认、失败重试、防重复提交、删除迟到响应',
 'role-editor':'基本字段、声音筛选/试听、语速音调、模型记忆、列表失败重试、保存载荷',
 'role-lifecycle':'角色新建/保存/放弃/切换/删除、保存失败阻止切换',
 'role-resources':'知识库及文件增删改、上传失败重试、移除与删除区别、单一上传入口、MCP/插件管理',
 'role-selection':'点击预览与绑定分离、切换后详情同步、描述展示',
 'room-flow':'6位数字房间号、4位数字密码、创建/加入/退出确认、失败恢复、状态更新失败、各设备状态'
};
const results=[],started=new Date().toISOString();
for(const file of fs.readdirSync(__dirname).filter(n=>n.endsWith('.browser.cjs')).sort()){
 const start=Date.now();const run=spawnSync(process.execPath,[path.join(__dirname,file)],{env:{...process.env,STAGE:'final'},encoding:'utf8',timeout:240000,maxBuffer:5*1024*1024});
 const log=(run.stdout||'')+(run.stderr||'')+(run.error?'\n'+run.error.message:'');fs.writeFileSync(path.join(out,file+'.log'),log);
 const result={suite:file,coverage:coverage[file.replace('.browser.cjs','')]||'见测试文件',status:run.status===0?'pass':'fail',durationMs:Date.now()-start,log:file+'.log'};
 results.push(result);console.log(result.status.toUpperCase(),file,Math.round(result.durationMs/1000)+'s');
 fs.writeFileSync(path.join(out,'acceptance-browser-results.json'),JSON.stringify({started,results},null,2));
}
const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const failed=results.filter(r=>r.status!=='pass');
fs.writeFileSync(path.join(out,'acceptance-report.html'),`<!doctype html><html lang="zh-CN"><meta charset="UTF-8"><meta name="viewport" content="width=device-width"><title>小钛用户端验收记录</title><style>body{font:16px/1.7 system-ui;margin:32px auto;padding:0 16px;max-width:1100px;color:#182426}table{border-collapse:collapse;width:100%}td,th{border:1px solid #ddd;padding:10px;text-align:left}a{color:#00856f}.pass{color:#00856f}.fail{color:#c33}code{word-break:break-all}</style><h1>用户端浏览器回归</h1><p>执行时间：${esc(started)}。${results.length} 组，${results.length-failed.length} 组通过，${failed.length} 组失败。本报告不把测试组数等同于功能点数量。</p><p><strong>环境：</strong>本地构建产物、真实 Chromium 浏览器、模拟接口及合成测试数据。手机为视口模拟，非真机；第三方验证码测试中的模拟与线上检查应分别查看。</p><table><tr><th>测试组 / 日志</th><th>覆盖内容</th><th>结果</th></tr>${results.map(r=>`<tr><td><a href="${esc(r.log)}">${esc(r.suite)}</a></td><td>${esc(r.coverage)}</td><td class="${r.status}">${r.status==='pass'?'通过':'失败'} (${Math.round(r.durationMs/1000)}s)</td></tr>`).join('')}</table><h2>必须单独验收的范围</h2><ul><li>线上登录后的真实业务：需要专用测试账号及获准操作的测试数据。</li><li>真实设备音视频、麦克风、旧设备协议兼容、设备插件执行及多人对讲。</li><li>真实邮件投递、知识库解析检索、云端 MCP 执行及第三方验证码验证通过后的完整链路。</li><li>iOS Safari、Android 真机与微信内嵌浏览器；本地宽度模拟不能替代真机。</li></ul><p>上述项目未由本地浏览器回归证明通过。线上只读检查另见 <a href="live-public-results.json">线上公开页面结果</a>，请核对该文件时间，不能据此推断当前修改已部署。</p></html>`);
console.log(`Browser regression: ${results.length-failed.length}/${results.length} passed. Report: ${path.join(out,'acceptance-report.html')}`);if(failed.length)process.exitCode=1;
