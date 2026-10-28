// Device-owned recognition preferences; explicit loading only, no background polling.
(() => {
 const deviceSessions=new Map();
 const outputNames={text:'文字',audio:'语音',text_audio:'文字＋语音'};
 let dialog, current, catalog=[], selected=[], generation=0;
 const el=(tag,text,className)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(className)n.className=className;return n;};
 const button=(text,fn)=>{const n=el('button',text);n.type='button';n.onclick=fn;return n;};
 async function request(path,options){const response=await api('/v1/recognition'+path,options);if(response.code!==200)throw new Error(response.msg||'识别设置暂不可用');return response.data;}
 function closeDialog(){generation++;dialog.close();}
 function move(id,target){const from=selected.findIndex(x=>x.scheme_id===id),to=selected.findIndex(x=>x.scheme_id===target);if(from<0||to<0||from===to)return;const [v]=selected.splice(from,1);selected.splice(to,0,v);render();}
 function render(){const pending=[];dialog.replaceChildren();const header=el('header');header.append(el('h2','AI识别设置'),button('关闭',closeDialog));dialog.append(header,el('p',current.device,'recognition-help'));const toggle=el('label',undefined,'recognition-line');toggle.append(el('span','开启AI识别'));const checkbox=el('input');checkbox.type='checkbox';checkbox.checked=current.enabled;checkbox.onchange=()=>{current.enabled=checkbox.checked;};toggle.append(checkbox);dialog.append(toggle,el('p','同一段音频依次分析。拖动手柄排序，或使用 ⋯ 菜单。保存后生效。','recognition-help'));
 const list=el('div');selected.forEach((type,index)=>{const row=el('div',undefined,'recognition-row');row.dataset.schemeId=type.scheme_id;const handle=button('⠿',()=>{});handle.className='recognition-handle';handle.setAttribute('aria-label','拖动排序 '+type.name);let target='';handle.onpointerdown=e=>{if(e.button!==0)return;handle.setPointerCapture(e.pointerId);target='';};handle.onpointermove=e=>{if(!handle.hasPointerCapture(e.pointerId))return;list.querySelectorAll('.recognition-target').forEach(x=>x.classList.remove('recognition-target'));const hit=document.elementFromPoint(e.clientX,e.clientY)?.closest('[data-scheme-id]');target=hit&&list.contains(hit)?hit.dataset.schemeId:'';if(target&&target!==type.scheme_id)hit.classList.add('recognition-target');};handle.onpointerup=e=>{handle.releasePointerCapture(e.pointerId);if(target)move(type.scheme_id,target);else render();};handle.onpointercancel=()=>render();const content=el('div');content.append(el('b',`${index+1}. ${type.name}`),el('small',`${outputNames[type.output_mode]} · 最大 ${type.limits.max_audio_seconds} 秒`));if(type.description)content.append(el('p',type.description,'recognition-description'));const more=el('details'),summary=el('summary','⋯');summary.setAttribute('aria-label','更多操作 '+type.name);const menu=el('div',undefined,'recognition-menu');const up=button('上移',()=>move(type.scheme_id,selected[index-1].scheme_id));up.disabled=index===0;const down=button('下移',()=>move(type.scheme_id,selected[index+1].scheme_id));down.disabled=index===selected.length-1;menu.append(up,down,button('移除',()=>{selected.splice(index,1);render();}));more.append(summary,menu);row.append(handle,content,more);list.append(row);});dialog.append(list);
 const add=button('＋ 添加音频类型',()=>{const picker=el('div',undefined,'recognition-picker');catalog.filter(x=>!selected.some(y=>y.scheme_id===x.scheme_id)).forEach(type=>{const label=el('label');const input=el('input');input.type='checkbox';input.onchange=()=>{if(input.checked)pending.push(type);else pending.splice(pending.indexOf(type),1);};const content=el('span',`${type.name} · ${outputNames[type.output_mode]}`);if(type.description)content.append(el('small',type.description,'recognition-description'));label.append(input,content);picker.append(label);});if(!picker.children.length)picker.append(el('p','没有更多可用类型','recognition-help'));picker.append(button('添加所选',()=>{if(selected.length+pending.length>8){toast('最多选择8个识别类型',true);return;}selected.push(...pending);render();}),button('取消',render));add.replaceWith(picker);});dialog.append(add,el('p',`本月已占用 ${current.state.usage.used} / ${current.state.policy.monthly_limit} 次，每个类型计一次。`,'recognition-help'));
 const actions=el('div',undefined,'recognition-line');
 const session=current, sessionGeneration=generation;
 const save=button(session.saving?'保存中…':'保存设置',async()=>{
   if(session.saving||sessionGeneration!==generation)return;
   const schemes=[...selected,...pending];
   if(schemes.length>8){toast('最多选择8个识别类型',true);return;}
   if(session.enabled&&!schemes.length){toast('请至少选择一个识别类型',true);return;}
   session.saving=true;save.disabled=true;
   try{
     const result=await request(`/devices/${encodeURIComponent(session.device)}/config`,{method:'PUT',body:JSON.stringify({enabled:session.enabled,schemes:schemes.map(x=>({scheme_id:x.scheme_id,scheme_version:x.scheme_version})),expected_revision:session.state.policy.revision})});
     if(deviceSessions.get(session.device)===sessionGeneration)document.querySelectorAll('[data-card-device]').forEach(card=>{if(card.dataset.cardDevice===session.device){const label=card.querySelector('[data-recognition]');if(label)label.textContent=result.policy.enabled?`已开启 · ${schemes.length}项`:'已关闭';}});
     if(sessionGeneration===generation){closeDialog();toast('AI识别设置已保存');}
   }catch(e){if(sessionGeneration===generation)toast(e.message,true);}
   finally{session.saving=false;if(sessionGeneration===generation){session.saveButton.disabled=false;session.saveButton.textContent='保存设置';}}
 });
 session.saveButton=save;save.disabled=session.saving;save.className='primary';actions.append(button('取消',closeDialog),save);dialog.append(actions);
 }
 window.openRecognition=async device=>{
   const requestGeneration=++generation;
   deviceSessions.set(device,requestGeneration);
   if(dialog?.open)dialog.close();
   try{
     const [types,state]=await Promise.all([request('/types'),request(`/devices/${encodeURIComponent(device)}/config`)]);
     if(requestGeneration!==generation)return;
     catalog=types;selected=state.schemes.map(x=>({...x}));current={device,state,enabled:state.policy.enabled,saving:false};
     if(!dialog){dialog=el('dialog',undefined,'recognition-panel');dialog.oncancel=()=>{generation++;};document.body.append(dialog);}
     render();if(!dialog.open)dialog.showModal();
   }catch(e){if(requestGeneration===generation)toast(e.message,true);}
 };
})();
