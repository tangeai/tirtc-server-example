// Synthetic API interactions; no messages or mutations reach deployed services.
module.exports = async (page, base) => {
 page.setDefaultTimeout(7000);
 const passed=[];
 const go=async route=>{await page.goto(base+'/admin/'+route);await page.locator('body').waitFor();await page.waitForLoadState('networkidle');await page.locator('body').ariaSnapshot();};
 const waitReq=(path,method)=>page.waitForFunction(({path,method})=>window.__adminRequests.some(r=>r.path===path&&r.method===method),{path,method},{timeout:7000});
 await go('boards');await page.getByRole('button',{name:/复制/}).click();await waitReq('/boards','POST');passed.push('开发板复制创建草稿');
 await go('users');await page.getByRole('button',{name:'调整额度',exact:false}).first().click();await page.getByRole('dialog').ariaSnapshot();await page.getByLabel('剩余绑定额度').fill('777');await page.getByRole('button',{name:/^(Close|关闭)$/}).click();await page.getByRole('button',{name:'调整额度',exact:false}).first().click();if(await page.getByLabel('剩余绑定额度').inputValue()!=='8')throw Error('额度沿用未保存草稿');passed.push('额度弹窗关闭重开不沿用未保存数据');
 await go('devices');await page.getByRole('button',{name:/导入设备池/}).click();await page.getByRole('dialog').ariaSnapshot();await page.locator('input[type=file]').setInputFiles({name:'invalid.csv',mimeType:'text/csv',buffer:Buffer.from('wrong,header\nfoo,bar')});await page.getByLabel(/原因/).fill('浏览器验收');await page.getByRole('dialog').getByRole('button',{name:/创建|导入/}).last().click();await page.getByText('CSV 前两列表头必须是 device_id,device_key',{exact:false}).first().waitFor();if(await page.evaluate(()=>window.__adminRequests.some(r=>r.path==='/device-pool/imports'&&r.method==='POST')))throw Error('无效CSV仍发送');passed.push('错误CSV在提交前阻止');
 const template={namespace:'user-server',config_key:'email.template.registration_code',group:'email_template',name:'注册验证码邮件',default:{enabled:true,subject:'验证码',html_body:'<p>{{code}}</p>',text_body:'{{code}}'}};
 await page.route('**/v1/admin/config-definitions?namespace=user-server',route=>route.fulfill({json:{code:200,data:{items:[template]}}}));
 await page.route('**/v1/admin/configs?namespace=user-server',route=>route.fulfill({json:{code:200,data:{items:[{...template,id:1,value:template.default,revision:1,status:1},{id:2,config_key:'smtp',value:{enabled:true},revision:1,status:1}]}}}));
 await go('configs/user-server');await page.getByRole('tab',{name:'邮件模板',exact:false}).click();await page.getByRole('button',{name:/编辑/}).click();await page.getByRole('dialog').ariaSnapshot();if(!await page.frameLocator('iframe[title="邮件 HTML 预览"]').getByText('123456').isVisible())throw Error('模板变量未预览');await page.getByLabel('发布原因').fill('浏览器验收');await page.getByRole('button',{name:'校验并发布',exact:false}).click();await waitReq('/configs/user-server/email.template.registration_code','PUT');passed.push('邮件模板变量预览与发布');
 await page.getByRole('button',{name:'发送测试',exact:false}).click();await page.getByLabel('测试收件邮箱').fill('fixture@example.test');await page.getByLabel('测试原因').fill('浏览器验收');await page.getByRole('button',{name:'发送测试邮件',exact:false}).click();await waitReq('/configs/user-server/email.template.registration_code/test','POST');passed.push('邮件测试表单提交（隔离接口，无真实发送）');
 return passed;
}
;
