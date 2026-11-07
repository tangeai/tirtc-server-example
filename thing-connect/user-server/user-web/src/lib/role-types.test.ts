import assert from 'node:assert/strict';
import { test } from 'node:test';
import { availableRoleTypes, roleTypePatch, type RoleType } from './role-types.ts';
import { draftOf, rolePayload } from './roles.ts';
const first: RoleType = { id:'one',name:'类型一',voice_id:'v1',voice_model:'cosyvoice-v3-flash',prompt:'默认设定一',sort_order:1,revision:1 };
const second: RoleType = {...first,id:'two',name:'类型二',voice_id:'v2',prompt:'默认设定二'};
test('template defaults initialize new drafts and create payload retains edited prompt',()=>{
 const draft={...draftOf({}),...roleTypePatch(draftOf({}),first,undefined,false)};
 assert.equal(draft.name,'类型一');assert.equal(draft.voice,'v1');draft.prompt='我手写的内容';draft.name='我的角色';
 const next={...draft,...roleTypePatch(draft,second,first,true)};
 assert.equal(next.name,'我的角色');assert.equal(next.prompt,'我手写的内容');assert.equal(next.voice,'v2');
 const payload=rolePayload({},next,[{id:'v2',name:'音色',model:first.voice_model}]);assert.equal(payload.agent_config?.prompt,'我手写的内容');assert.equal(payload.service_config?.config?.tts?.provider_params?.voice,'v2');
});
test('switching unedited defaults updates name and prompt without changing unrelated settings',()=>{
 const draft={...draftOf({}),name:first.name,prompt:first.prompt,memory:true,knowledge:'kb'};
 const next={...draft,...roleTypePatch(draft,second,first,false)};
 assert.equal(next.name,second.name);assert.equal(next.prompt,second.prompt);assert.equal(next.memory,true);assert.equal(next.knowledge,'kb');assert.equal(draft.prompt,first.prompt);
});
test('only supported and available voices can be chosen; templates sharing a voice remain distinct',()=>{
 const duplicate={...second,voice_id:first.voice_id};
 assert.deepEqual(availableRoleTypes([first,second,duplicate],[{id:'v1',name:'voice',model:first.voice_model}]).map(x=>x.id),['one','two']);
 assert.equal(availableRoleTypes([first],[{id:'v1',name:'voice',model:'unsupported'}]).length,0);
});
