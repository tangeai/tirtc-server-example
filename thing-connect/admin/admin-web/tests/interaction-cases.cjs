// Synthetic API interactions; no messages or mutations reach deployed services.
module.exports = async (page, base) => {
 page.setDefaultTimeout(7000);
 const passed=[];
 const check=(value,label)=>{if(!value)throw Error(label);passed.push(label);};
 const go=async route=>{await page.goto(base+'/admin/'+route);await page.locator('body').waitFor();await page.waitForFunction(()=>!document.querySelector('.ant-spin-spinning'),null,{timeout:8000});await page.locator('body').ariaSnapshot();};
 const requests=()=>page.evaluate(()=>window.__adminRequests);
 const waitRequest=async(path,query='',method='GET')=>page.waitForFunction(({path,query,method})=>window.__adminRequests.some(r=>r.path===path&&r.method===method&&r.query.includes(query)),{path,query,method},{timeout:7000});
 try {
 await page.setViewportSize({width:1440,height:1000});
 await go('admin-users');
 await page.getByTitle('2',{exact:false}).click();await waitRequest('/admin-users','page=2');check(true,'管理员列表服务端第二页');
 await page.getByRole('button',{name:/编\s*辑$/}).first().click();await page.getByRole('dialog').ariaSnapshot();
 await page.getByLabel('昵称',{exact:false}).fill('');await page.getByRole('button',{name:/保\s*存$/}).click();
 check(!(await requests()).some(r=>r.method==='PUT'),'管理员表单必填校验阻止提交');
 await page.getByLabel('昵称',{exact:false}).fill('验收编辑');await page.getByLabel('操作原因',{exact:false}).fill('浏览器自测');
 await page.getByRole('button',{name:/保\s*存$/}).click();await page.getByRole('dialog').waitFor({state:'hidden'});check((await requests()).some(r=>r.method==='PUT'&&r.body.includes('验收编辑')),'管理员编辑保存');
 await go('users');await page.getByLabel('用户',{exact:false}).fill('fixture');await page.getByRole('button',{name:/查\s*询$/}).click();await waitRequest('/users','keyword=fixture');check(true,'用户筛选提交');
 await page.getByRole('button',{name:/重\s*置$/}).click();await page.waitForFunction(()=>window.__adminRequests.filter(r=>r.path==='/users').at(-1).query.includes('keyword=&'),{},{timeout:7000});check(true,'用户筛选重置');
 await page.getByRole('button',{name:'详情',exact:false}).first().click();await page.getByRole('dialog').ariaSnapshot();check(await page.getByRole('dialog').getByText('中国电信',{exact:false}).count()>0,'用户详情中文IP');await page.getByRole('button',{name:/^(关闭|Close)$/}).click();
 await go('jobs');await page.getByTitle('2',{exact:false}).click();await waitRequest('/jobs','page=2');check(true,'任务列表服务端第二页');
 await page.getByRole('button',{name:'详情',exact:false}).first().click();await page.getByRole('dialog').ariaSnapshot();await page.getByRole('dialog').getByTitle('2',{exact:false}).click();await waitRequest('/jobs/21','page=2');check(true,'任务逐行明细服务端第二页');await page.getByRole('button',{name:/^(关闭|Close)$/}).click();
 await go('dictionaries');await page.getByText('设备类型',{exact:false}).click();await page.getByText('设备区域',{exact:false}).click();await page.getByText('深圳',{exact:false}).waitFor();await page.waitForTimeout(800);check(await page.getByText('摄像头',{exact:false}).count()===0,'字典切换忽略旧请求');
 await page.getByRole('button',{name:'新增字典项',exact:false}).click();await page.getByRole('dialog').ariaSnapshot();await page.getByLabel('显示名称',{exact:false}).fill('广州');await page.getByLabel('接口取值',{exact:false}).fill('guangzhou');await page.getByLabel('操作原因',{exact:false}).fill('浏览器自测');await page.getByRole('button',{name:/保\s*存$/}).click();await waitRequest('/dict-types/b/items','','POST');check(true,'字典项保存到当前字典');
 await go('recognition');await page.getByPlaceholder('设备 ID',{exact:false}).fill('DEVICE-A');await page.getByRole('button',{name:/查\s*询$/}).click();await page.getByPlaceholder('设备 ID',{exact:false}).fill('DEVICE-B');await page.getByRole('button',{name:/查\s*询$/}).click();await page.getByLabel('每月识别次数上限（每执行一个类型计一次）').waitFor();await page.waitForTimeout(800);check(await page.getByLabel('每月识别次数上限（每执行一个类型计一次）').inputValue()==='200','设备B额度未被设备A迟到响应覆盖');
 await page.getByLabel('修改原因',{exact:false}).fill('额度验收');await page.getByRole('button',{name:'保存上限',exact:false}).click();await waitRequest('/devices/DEVICE-B/recognition','','PUT');check(true,'识别额度保存到已查询设备');
 await page.getByPlaceholder('设备 ID',{exact:false}).fill('DEVICE-C');check(await page.getByRole('button',{name:'保存上限',exact:false}).count()===0,'更换设备后隐藏旧额度表单');
 await page.getByRole('tab',{name:'识别记录',exact:false}).click();await page.getByRole('button',{name:/查\s*询$/,exact:false}).click();await page.getByRole('button',{name:/查\s*看$/}).click();await page.getByRole('dialog').ariaSnapshot();check(await page.getByText('检测到环境声音',{exact:false}).isVisible(),'识别结果结构化展示');await page.getByRole('button',{name:/^(关闭|Close)$/}).click();
 await go('login-logs');await page.getByPlaceholder('管理员邮箱',{exact:false}).fill('filter@example.test');await page.getByRole('button',{name:/查\s*询$/}).click();await waitRequest('/login-logs','email=filter');check(true,'日志查询筛选');await page.getByRole('button',{name:/重\s*置$/}).click();check(await page.getByPlaceholder('管理员邮箱',{exact:false}).inputValue()==='','日志筛选重置');
 await go('users');await page.route('**/v1/admin/users?**',route=>route.fulfill({status:503,json:{code:503,msg:'测试依赖暂不可用'}}),{times:1});await page.getByRole('button',{name:/刷\s*新$/}).click();await page.getByText('测试依赖暂不可用',{exact:false}).first().waitFor();check(await page.locator('body').getByText('user@example.test',{exact:false}).count()===0,'请求失败不显示旧数据冒充新结果');
 return {passed};
 } catch(error) {return {passed,error:String(error),snapshot:await page.locator("body").ariaSnapshot()};}
}
;
