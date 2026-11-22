// Synthetic API fixtures for browser acceptance. Unknown endpoints fail closed.
module.exports = async (page, allowedPaths) => {
 const paths=[['/overview','数据概览',0],['/users','用户管理',100],['/devices','设备管理',100],['/voip-devices','VoIP 授权设备',100],['/boards','开发板管理',100],['/recognition','AI识别',100],['/configs/device-server','设备服务',101],['/configs/user-server','用户服务',101],['/configs/voip-server','VoIP 服务',101],['/configs/ai-server','AI 服务',101],['/configs/call-server','呼叫服务',101],['/configs/recognition-server','识别服务',101],['/configs/common','通用配置',101],['/configs/system','系统配置',101],['/admin-users','管理员',102],['/access','权限与菜单',102],['/dictionaries','数据字典',102],['/jobs','任务中心',103],['/login-logs','登录日志',103],['/audit-logs','操作日志',103]];
 const menus=[...['业务管理','服务配置','访问控制','运维审计'].map((name,i)=>({id:100+i,parent_id:0,name,menu_type:1,menu_code:'group'+i,sort_no:i})),...paths.map(([path,name,parent_id],i)=>({id:i+1,parent_id,path,name,menu_type:2,menu_code:path.slice(1),sort_no:i}))];
 const time='2026-11-13T10:00:00Z';
 const user={id:1,email:'user@example.test',status:1,device_count:2,bind_quota:8,created_at:time,last_login_at:time,registration_ip:'119.123.132.48',registration_ip_location:{status:'found',region:'中国–广东–深圳',isp:'中国电信'},last_login_ip:'240e:1234:5678:90ab:cdef:1234:5678:90ab',last_login_ip_location:{status:'found',region:'中国–广东–深圳',isp:'中国电信'}};
 const device={id:1,device_id:'DEVICE-A',user_id:1,user_email:user.email,device_name:'测试设备',mac:'AA:BB:CC:DD:EE:FF',online:false,presence_known:true,last_online_ip:user.registration_ip,last_online_ip_location:user.registration_ip_location,last_online_at:time,active_time:time,bind_time:time,hardware:{chip_model:'ESP32-S3',board_model:'Demo'},profiles:{stream:{audio_rate:16000}},firmware_version:'1.0.0'};
 const role={id:1,code:'support',name:'技术支持',status:1,permissions:['device.read'],effective_permissions:['device.read'],parent_id:0,sort_no:1};
 const admin={id:1,email:'admin@example.test',nick_name:'验收管理员',status:1,mfa_enabled:1,last_login_at:time,roles:['support']};
 const job={id:1,job_type:'device_pool_import',status:4,total_count:45,succeeded_count:40,failed_count:5,attempts:1,source_name:'fixture.csv',updated_at:time,result_available:false};
 const instance={instance_id:'node-01-service',node:'node-01',zone:'cn-shenzhen',version:'test',status:'healthy',dependencies:{mysql:'healthy',redis:'healthy'},config_revision:{'web.site':2,'mqtt.connection':5},started_at:time,last_heartbeat:time};
 const service=name=>({service:name,status:'healthy',instance_count:1,healthy_count:1,instances:[instance],configuration_ready:true,required_configurations:[],start_command:'service-local.sh start '+name,restart_command:'service-local.sh restart '+name});
 const scheme={scheme_id:'audio',scheme_version:1,revision:1,name:'声音分析',description:'测试用途',media_type:'audio',model:'qwen3-omni-flash',prompt:'分析声音',output_mode:'text_audio',voice:'Cherry',limits:{max_file_bytes:5242880,max_audio_seconds:15},enabled:true};
 const state={policy:{enabled:true,allowed:true,monthly_limit:100,revision:1,schemes:[{scheme_id:'audio',scheme_version:1}]},schemes:[scheme],usage:{used:5,month:'2026-11',input_tokens:100,output_tokens:20,unknown_calls:0,tts_characters:80}};
 const definition=namespace=>({namespace,config_key:'web.site',group:'web',name:'站点名称',description:'用户看到的站点名称',default:{name:'ThingConnect'},fields:[{path:['name'],label:'站点名称',kind:'text',required:true}],required:false,blocking:false,targets:[namespace],reload:'runtime'});
 await page.addInitScript(()=>{window.__adminRequests=[];window.__adminErrors=[];window.addEventListener('error',e=>window.__adminErrors.push(e.message));});
 await page.route('**/v1/**',async route=>{
  const req=route.request(),url=new URL(req.url()),p=url.pathname.replace('/v1/admin',''),method=req.method();
  const call={path:p,query:url.search,method,body:method==='GET'?null:req.postData()};
  await page.evaluate(call=>window.__adminRequests?.push(call),call).catch(()=>{});
  let data, status=200;
  const pageNo=Number(url.searchParams.get('page')||1),pageSize=Number(url.searchParams.get('page_size')||20);
  const paged=(row,total=45)=>({items:Array.from({length:Math.max(0,Math.min(pageSize,total-(pageNo-1)*pageSize))},(_,i)=>({...row,id:(pageNo-1)*pageSize+i+1})),total,page:pageNo,page_size:pageSize});
  if(p==='/v1/setup/status') data={mode:'normal'};
  else if(p==='/auth/refresh') data={access_token:'fixture-only'};
  else if(p==='/me') data={user:{...admin,must_change_password:0}};
  else if(p==='/me/navigation'||p==='/menus') data=p==='/menus'?{items:menus}:{menus:allowedPaths?menus.filter(m=>m.menu_type!==2||allowedPaths.includes(m.path)):menus};
  else if(p==='/permissions') data={permissions:['*']};
  else if(method!=='GET') data={...state,id:1,revision:2,job_id:1};
  else if(p==='/services/status') data={services:['device-server','user-server','voip-server','ai-server','call-server','recognition-server'].map(service),metrics:{user_count:1234,bound_devices:10000,available_pool:500,binds_24h:120,unbinds_24h:5,cleanup_pending:0,failed_admin_jobs:2},recent_audits:[{id:1,created_at:time,email:admin.email,action:'config.publish',resource_type:'config',resource_id:'web.site'}]};
  else if(/^\/services\/.+\/status$/.test(p)) data=service(p.split('/')[2]);
  else if(p==='/users') data=paged(user);
  else if(/^\/users\/\d+$/.test(p)) data={user,devices:[device],bind_logs:[],ai:{role_count:2,resource_count:1}};
  else if(p==='/devices') data={...paged(device),items:[device],total:1};
  else if(p.endsWith('/bind-logs')) data=paged({device_id:'DEVICE-A',created_at:time,action:1},2);
  else if(p.endsWith('/recognition')) {if(p.includes('DEVICE-A')) await new Promise(r=>setTimeout(r,700)); data={...state,policy:{...state.policy,monthly_limit:p.includes('DEVICE-B')?200:100}};}
  else if(/^\/devices\/[^/]+$/.test(p)) data=device;
  else if(p==='/device-pool') data=paged({device_id:'POOL-DEVICE',status:0,ever_bound:false,created_at:time},1);
  else if(p==='/boards') data={items:[{id:'board-1',name:'测试开发板',vendor:'测试厂商',model:'DEMO-S3',chip:'ESP32-S3',summary:'测试用开发板',capabilities:['实时音视频'],adaptation_status:'ready',publish_status:'draft',image_url:'',detail_slug:'demo-s3',revision:1,sort_order:1}]};
  else if(p==='/admin-users') data=paged(admin);
  else if(p.endsWith('/sessions')) data={items:[{id:1,family_id:'test-session',created_at:time,expires_at:'2027-01-01T00:00:00Z'}]};
  else if(p==='/roles') data={items:[role],registered_permissions:['device.read'],permission_definitions:[{code:'device.read',name:'查看设备',group:'设备管理',description:'读取设备资料'}]};
  else if(p.endsWith('/menus')) data={menu_ids:[1,2]};
  else if(p==='/dict-types') data={items:[{id:1,code:'a',name:'设备类型',status:1},{id:2,code:'b',name:'设备区域',status:1}]};
  else if(/^\/dict-types\/[^/]+\/items$/.test(p)) {if(p.includes('/a/')) await new Promise(r=>setTimeout(r,700));data={items:[{id:1,label:p.includes('/a/')?'摄像头':'深圳',value:p.includes('/a/')?'camera':'shenzhen',sort_no:1,status:1,is_default:1}]};}
  else if(p==='/jobs') data={jobs:paged(job),related_queues:{cleanup_pending:0,config_publish_pending:0}};
  else if(/^\/jobs\/\d+$/.test(p)) data={job,items:paged({row_no:pageNo*20,resource_id:'DEVICE-A',status:1,error_message:''}).items,page:pageNo,page_size:pageSize};
  else if(p==='/login-logs') data=paged({created_at:time,email:admin.email,status:1,client_ip:'119.123.132.48',message:'success'});
  else if(p==='/audit-logs') data=paged({created_at:time,email:admin.email,admin_user_id:1,action:'config.publish',resource_type:'config',resource_id:'web.site',reason:'验收数据',request_id:'test-request'});
  else if(p==='/config-definitions') data={items:[definition(url.searchParams.get('namespace'))]};
  else if(p==='/configs') data={items:[{id:1,namespace:url.searchParams.get('namespace'),config_key:'web.site',value:{name:'ThingConnect'},revision:2,status:1,secret_configured:false,using_default:false}]};
  else if(p==='/recognition/schemes') data=[scheme];
  else if(p==='/recognition/requests') data=[{task:{request_id:'request-1',device_id:'DEVICE-A',status:'succeeded',results:[{item_id:'item-1',scheme_id:'audio',status:'succeeded',result:{summary:'检测到环境声音'}}]},calls:[]}];
  else if(p==='/ai-role-types') data={items:[{id:'role-1',name:'语音助手',language:'zh',voice_id:'longxiaochun',voice_model:'cosyvoice-v3-flash',prompt:'你是一个助手',sort_order:1,status:'published',revision:1}]};
  else if(p==='/ai-role-types/voices') data={items:[{id:'longxiaochun',name:'小春',model:'cosyvoice-v3-flash',languages:['zh']}]};
  else if(p==='/voip/apps') data={items:[{app_id:'wx-test',model_id:'test-model',enabled:true,is_default:true,active_auth_count:1,invalid_auth_count:0,config_status:'healthy',secret_configured:true}],revision:1};
  else if(p.startsWith('/voip/apps/')&&p.endsWith('/devices')) data=paged({...device,auth_status:'active',profile_reported:true,profile_updated_at:time},1);
  else if(p.startsWith('/voip/devices/')) data={...device,profile:{audio_rate:16000},attributes:{}};
  else {status=404;data={path:p};}
  await route.fulfill({status,json:{code:status,msg:status===200?'ok':'未准备测试接口',data}});
 });
 return paths;
}
;
