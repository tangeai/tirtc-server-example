import test from 'node:test';
import assert from 'node:assert/strict';
import { draftOf, rolePayload, matchingVoices, type Role } from './roles.ts';
const role: Role = { id:'r', name:'原角色', parent_role_id:'parent', avatar:'icon', service_config:{name:'custom',type:'custom-compose',config:{llm:{provider:'ali-llm',provider_params:{model:'custom-model',temperature:0.7}},tts:{provider:'ali-tts',provider_params:{model:'old-model',voice:'unknown',rate:1.2,pitch:0.8,other:true}},asr:{provider:'other'}}},agent_config:{prompt:'hello',welcome_text:'',ali_memory:{enable:true,retention:20},ali_rag:{index_id:'missing-index',unknown:true},mcp_tools:['unavailable-tool'],device_plugins:['unavailable-plugin'],opaque:{keep:true}},user_params:{description:'d',custom:'keep'} };
test('renaming preserves providers and unavailable resources without fetched option lists',()=>{const draft=draftOf(role);draft.name='新名称';const result=rolePayload(role,draft,[]);assert.deepEqual(result.service_config,role.service_config);assert.deepEqual(result.agent_config,role.agent_config);assert.equal(result.user_params?.custom,'keep');assert.equal(role.name,'原角色');});
test('noncustom services remain opaque even if draft provider fields change',()=>{const original={...role,service_config:{type:'external',config:{llm:{provider:'other',provider_params:{secret:'opaque'}}}}};const draft=draftOf(original);draft.model='new';draft.voice='new';assert.deepEqual(rolePayload(original,draft,[]).service_config,original.service_config);});
test('clearing overrides inherits defaults while preserving unrelated components',()=>{const draft=draftOf(role);draft.model='';draft.voice='';draft.knowledge='';draft.mcp=[];draft.plugins=[];const saved=rolePayload(role,draft,[]);assert.equal(saved.service_config?.config?.llm,undefined);assert.equal(saved.service_config?.config?.tts,undefined);assert.deepEqual(saved.service_config?.config?.asr,role.service_config?.config?.asr);assert.equal(saved.agent_config?.ali_rag,null);assert.deepEqual(saved.agent_config?.mcp_tools,[]);});
test('changing voice rejects incompatible provider models and preserves unknown parameters',()=>{const draft=draftOf(role);draft.voice='new';assert.throws(()=>rolePayload(role,draft,[{id:'new',name:'new',model:'unsupported'}]));const saved=rolePayload(role,draft,[{id:'new',name:'new',model:'cosyvoice-v3-flash'}]);assert.equal(saved.service_config?.config?.tts?.provider_params?.other,true);assert.equal(saved.service_config?.config?.tts?.provider_params?.model,'cosyvoice-v3-flash');});
test('validates names by Unicode characters',()=>{const draft=draftOf(role);draft.name='😀'.repeat(64);assert.equal(rolePayload(role,draft,[]).name,draft.name);draft.name+='😀';assert.throws(()=>rolePayload(role,draft,[]));draft.name=' ';assert.throws(()=>rolePayload(role,draft,[]));});

test('voice language filtering accepts regional tags without mixing engines',()=>{
 const voices=[{id:'a',name:'中文声音',model:'cosyvoice-v3-flash',languages:['zh-CN']},{id:'b',name:'English',model:'cosyvoice-v3-flash',languages:['en_US']},{id:'c',name:'其他引擎',model:'other',languages:['zh']}];
 assert.deepEqual(matchingVoices(voices,'zh','').map(v=>v.id),['a']);
 assert.deepEqual(matchingVoices(voices,'en',' english ').map(v=>v.id),['b']);
 assert.deepEqual(matchingVoices(voices,'','不存在'),[]);
});

test('renaming preserves inherited model and voice component options without explicit overrides',()=>{
 const inherited:Role={id:'inherited',name:'继承配置',service_config:{type:'custom-compose',config:{llm:{provider:'ali-llm',provider_params:{temperature:0.4}},tts:{provider:'ali-tts',provider_params:{model:'cosyvoice-v3-flash',rate:1.2,opaque:'keep'}}}}};
 const draft=draftOf(inherited);draft.name='只改名字';
 assert.deepEqual(rolePayload(inherited,draft,[]).service_config,inherited.service_config);
});
