import assert from 'node:assert/strict';
import { test } from 'node:test';
import { availableRoleTypes, roleTypePatch, selectedRoleType, type RoleType } from '../src/domain/role-types.ts';
import { draftOf, rolePayload } from '../src/domain/roles.ts';
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

test('saved type restores on edit without overwriting custom prompts; shared voices cannot infer a type', () => {
 const draft={...draftOf({}),...roleTypePatch(draftOf({}),first,undefined,false),prompt:'用户修改',customPrompt:true};
 const saved=rolePayload({},draft,[{id:'v1',name:'voice',model:first.voice_model}]);
 const reopened=draftOf(saved);
 assert.equal(selectedRoleType(reopened,[first,second])?.id,first.id);
 assert.equal(reopened.prompt,'用户修改');assert.equal(reopened.customPrompt,true);
 const legacy=draftOf({agent_config:{prompt:'手写设定'},user_params:{voice:first.voice_id}});
 assert.equal(selectedRoleType(legacy,[first,second]),undefined);
 assert.equal(selectedRoleType({...legacy,prompt:first.prompt},[first,second])?.id,first.id);
 assert.equal(selectedRoleType(reopened,[])?.id,undefined);assert.equal(reopened.prompt,'用户修改');
});

test('all documented TTS models keep independently selected LLM and exact voice pairs across save and reopen', () => {
 const models = ['cosyvoice-v3', 'cosyvoice-v3-flash', 'seed-tts-2.0'];
 const voices = models.map(model => ({ id:'shared', name:model, model }));
 let previous = {};
 for (const model of models) {
  const type = {...first, id:model, voice_id:'shared', voice_model:model};
  const draft = {...draftOf(previous), name:'测试角色', model:'doubao-seed-2-0-mini-260428'};
  const next = {...draft, ...roleTypePatch(draft, type, undefined, false)};
  assert.equal(next.model, draft.model);
  const saved = rolePayload(previous, next, voices);
  assert.equal(saved.service_config?.config?.tts?.provider_params?.model, model);
  assert.equal(saved.service_config?.config?.llm?.provider, 'doubao');
  const reopened = draftOf(saved);
  assert.equal(reopened.voiceModel, model);
  assert.equal(reopened.voice, 'shared');
  const llmChanged = rolePayload(saved, {...reopened, model:'qwen3.7-flash'}, voices);
  assert.deepEqual(llmChanged.service_config?.config?.tts, saved.service_config?.config?.tts);
  previous = saved;
 }
 const v3 = {...first, voice_model:'cosyvoice-v3'};
 assert.deepEqual(availableRoleTypes([v3], [{id:'v1', name:'Flash', model:'cosyvoice-v3-flash'}]), []);
 assert.equal(selectedRoleType({...draftOf({}),voice:'v1',prompt:first.prompt},[first,v3]),undefined);
 assert.equal(selectedRoleType({...draftOf({}),voice:'v1',voiceModel:'cosyvoice-v3',prompt:first.prompt},[first,v3]),v3);
});
