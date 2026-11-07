import test from 'node:test';
import assert from 'node:assert/strict';
import { draftOf, editableProviders, rolePayload, type Role } from './roles.ts';
const role: Role = { id:'r', name:'原角色', parent_role_id:'parent', avatar:'icon', service_config:{name:'custom',type:'custom-compose',config:{llm:{provider:'ali-llm',provider_params:{model:'custom-model',temperature:0.7}},tts:{provider:'ali-tts',provider_params:{model:'old-model',voice:'unknown',rate:1.2,pitch:0.8,other:true}},asr:{provider:'other'}}},agent_config:{prompt:'hello',welcome_text:'',ali_memory:{enable:true,retention:20},ali_rag:{index_id:'missing-index',unknown:true},mcp_tools:['unavailable-tool'],device_plugins:['unavailable-plugin'],opaque:{keep:true}},user_params:{description:'d',custom:'keep'} };
test('renaming preserves providers and unavailable resources without fetched option lists',()=>{const draft=draftOf(role);draft.name='新名称';const result=rolePayload(role,draft,[]);assert.deepEqual(result.service_config,role.service_config);assert.deepEqual(result.agent_config,role.agent_config);assert.equal(result.user_params?.custom,'keep');assert.equal(role.name,'原角色');});
test('noncustom services remain opaque even if draft provider fields change',()=>{const original={...role,service_config:{type:'external',config:{llm:{provider:'other',provider_params:{secret:'opaque'}}}}};const draft=draftOf(original);draft.model='new';draft.voice='new';assert.deepEqual(rolePayload(original,draft,[]).service_config,original.service_config);});
test('clearing overrides inherits defaults while preserving unrelated components',()=>{const draft=draftOf(role);draft.model='';draft.voice='';draft.knowledge='';draft.mcp=[];draft.plugins=[];const saved=rolePayload(role,draft,[]);assert.equal(saved.service_config?.config?.llm,undefined);assert.equal(saved.service_config?.config?.tts,undefined);assert.deepEqual(saved.service_config?.config?.asr,role.service_config?.config?.asr);assert.equal(saved.agent_config?.ali_rag,null);assert.deepEqual(saved.agent_config?.mcp_tools,[]);});
test('changing voice rejects incompatible provider models and preserves unknown parameters',()=>{const draft=draftOf(role);draft.voice='new';draft.voiceModel='cosyvoice-v3-flash';assert.throws(()=>rolePayload(role,draft,[{id:'new',name:'new',model:'unsupported'}]));const saved=rolePayload(role,draft,[{id:'new',name:'new',model:'cosyvoice-v3-flash'}]);assert.equal(saved.service_config?.config?.tts?.provider_params?.other,true);assert.equal(saved.service_config?.config?.tts?.provider_params?.model,'cosyvoice-v3-flash');});
test('validates names by Unicode characters',()=>{const draft=draftOf(role);draft.name='😀'.repeat(64);assert.equal(rolePayload(role,draft,[]).name,draft.name);draft.name+='😀';assert.throws(()=>rolePayload(role,draft,[]));draft.name=' ';assert.throws(()=>rolePayload(role,draft,[]));});

test('renaming preserves inherited model and voice component options without explicit overrides',()=>{
 const inherited:Role={id:'inherited',name:'继承配置',service_config:{type:'custom-compose',config:{llm:{provider:'ali-llm',provider_params:{temperature:0.4}},tts:{provider:'ali-tts',provider_params:{model:'cosyvoice-v3-flash',rate:1.2,opaque:'keep'}}}}};
 const draft=draftOf(inherited);draft.name='只改名字';
 assert.deepEqual(rolePayload(inherited,draft,[]).service_config,inherited.service_config);
});

test('Seed role creation and reopening use native audio parameters',()=>{
 const draft={...draftOf({}),name:'猴侠',voice:'seed-monkey',voiceModel:'seed-tts-2.0',rate:0,pitch:0};
 const voices=[{id:'seed-monkey',name:'猴哥2.0',model:'seed-tts-2.0'}];
 const saved=rolePayload({},draft,voices);
 assert.deepEqual(saved.service_config?.config?.tts,{provider:'ve-seed-tts-2',provider_params:{model:'seed-tts-2.0',audio:{voice_type:'seed-monkey',speech_rate:0,pitch_rate:0}}});
 const reopened=draftOf(saved);assert.equal(reopened.voice,'seed-monkey');assert.equal(reopened.rate,0);assert.equal(reopened.pitch,0);
 assert.deepEqual(rolePayload(saved,{...reopened,name:'改名'},[]).service_config,saved.service_config);
});
test('switching TTS providers removes old credentials and preserves unrelated components',()=>{
 const source={...role,service_config:{...role.service_config,config:{...role.service_config?.config,tts:{provider:'ali-tts',provider_params:{model:'cosyvoice-v3-flash',voice:'old',api_key:'old-provider-secret',volume:90}}}}};
 const draft={...draftOf(source),voice:'seed',voiceModel:'seed-tts-2.0',rate:0,pitch:-2};
 const saved=rolePayload(source,draft,[{id:'seed',name:'Seed',model:'seed-tts-2.0'}]);
 assert.equal(saved.service_config?.config?.tts?.provider_params?.api_key,undefined);
 assert.equal(saved.service_config?.config?.tts?.provider_params?.voice,undefined);
 assert.deepEqual(saved.service_config?.config?.asr,source.service_config?.config?.asr);
 const back=rolePayload(saved,{...draftOf(saved),voice:'ali',voiceModel:'cosyvoice-v3-flash',rate:1,pitch:1},[{id:'ali',name:'Ali',model:'cosyvoice-v3-flash'}]);
 assert.equal(back.service_config?.config?.tts?.provider,'ali-tts');assert.equal(back.service_config?.config?.tts?.provider_params?.audio,undefined);
});
test('Seed edits preserve audio extras and validate native ranges',()=>{
 const source:Role={name:'seed',service_config:{type:'custom-compose',config:{tts:{provider:'ve-seed-tts-2',provider_params:{model:'seed-tts-2.0',audio:{voice_type:'v',speech_rate:10,pitch_rate:2,format:'pcm'},resource_id:'seed-tts-2.0'}}}}};
 const draft={...draftOf(source),rate:20};
 const tts=rolePayload(source,draft,[]).service_config?.config?.tts;
 assert.equal(tts?.provider_params?.audio?.format,'pcm');assert.equal(tts?.provider_params?.audio?.speech_rate,20);
 assert.throws(()=>rolePayload(source,{...draft,pitch:13},[]));assert.throws(()=>rolePayload(source,{...draft,rate:0.5},[]));
});


test('Qwen Flash upgrade preserves same-provider credentials and settings',()=>{
 const original:Role={name:'千问角色',service_config:{type:'custom-compose',config:{llm:{provider:'ali-llm',provider_params:{model:'qwen3.5-flash',api_key:'synthetic-ali-key',base_url:'https://ali.example.test',temperature:0.3}}}}};
 const saved=rolePayload(original,{...draftOf(original),model:'qwen3.7-flash'},[]);
 assert.deepEqual(saved.service_config?.config?.llm,{provider:'ali-llm',provider_params:{model:'qwen3.7-flash',api_key:'synthetic-ali-key',base_url:'https://ali.example.test',temperature:0.3}});
});
test('LLM provider changes drop provider credentials, preserve voice and survive reopen',()=>{
 const original:Role={...role,service_config:{...role.service_config,config:{...role.service_config?.config,llm:{provider:'ali-llm',provider_params:{model:'qwen3.7-flash',api_key:'synthetic-ali-key',base_url:'https://ali.example.test',temperature:0.3},provider_extra:true}}}};
 const saved=rolePayload(original,{...draftOf(original),model:'doubao-seed-2-0-mini-260428'},[]);
 assert.deepEqual(saved.service_config?.config?.llm,{provider:'doubao',provider_params:{model:'doubao-seed-2-0-mini-260428'}});
 assert.deepEqual(saved.service_config?.config?.tts,original.service_config?.config?.tts);
 assert.deepEqual(saved.service_config?.config?.asr,original.service_config?.config?.asr);
 assert.equal(editableProviders(saved).llm,true);
 assert.deepEqual(rolePayload(saved,{...draftOf(saved),name:'改名'},[]).service_config,saved.service_config);
 const withCredentials:Role={...saved,service_config:{...saved.service_config,config:{...saved.service_config?.config,llm:{provider:'doubao',provider_params:{model:'doubao-seed-2-0-mini-260428',api_key:'synthetic-doubao-key',base_url:'https://doubao.example.test'}}}}};
 const back=rolePayload(withCredentials,{...draftOf(withCredentials),model:'qwen3.7-flash'},[]);
 assert.deepEqual(back.service_config?.config?.llm,{provider:'ali-llm',provider_params:{model:'qwen3.7-flash'}});
 const inherited=rolePayload(saved,{...draftOf(saved),model:''},[]);assert.equal(inherited.service_config?.config?.llm,undefined);
});
test('new roles can select either new LLM and unknown choices fail',()=>{
 for(const [model,provider] of [['qwen3.7-flash','ali-llm'],['doubao-seed-2-0-mini-260428','doubao']]){
  const saved=rolePayload({},{...draftOf({}),name:'新角色',model},[]);assert.deepEqual(saved.service_config?.config?.llm,{provider,provider_params:{model}});
 }
 assert.throws(()=>rolePayload({},{...draftOf({}),name:'新角色',model:'unknown-model'},[]));
});

test('every LLM and TTS combination survives save, reopen and independent model changes', () => {
 const llms = ['qwen3.5-flash', 'qwen3.7-flash', 'qwen3.7-plus', 'doubao-seed-2-0-mini-260428'];
 const models = ['cosyvoice-v3', 'cosyvoice-v3-flash', 'seed-tts-2.0'];
 const voices = models.map(model => ({id:'shared-id', name:model, model}));
 for (const model of llms) for (const voiceModel of models) {
  const seed = voiceModel === 'seed-tts-2.0';
  const draft = {...draftOf({}), name:'组合测试', model, voiceModel, voice:'shared-id', rate:seed?0:1, pitch:seed?0:1, prompt:'手写设定'};
  const saved = rolePayload({}, draft, voices);
  assert.equal(saved.service_config?.config?.llm?.provider, model.startsWith('qwen')?'ali-llm':'doubao');
  assert.equal(saved.service_config?.config?.tts?.provider, seed?'ve-seed-tts-2':'ali-tts');
  assert.equal(saved.service_config?.config?.tts?.provider_params?.model, voiceModel);
  const reopened = draftOf(saved);
  assert.equal(reopened.model, model); assert.equal(reopened.voiceModel, voiceModel);
  assert.equal(reopened.prompt, '手写设定');
  for (const nextModel of llms) {
   const next = rolePayload(saved, {...reopened, model:nextModel}, []);
   assert.deepEqual(next.service_config?.config?.tts, saved.service_config?.config?.tts);
   assert.equal(next.agent_config?.prompt, '手写设定');
  }
 }
});

test('ambiguous or missing model pairs cannot silently select another engine', () => {
 const voices = ['cosyvoice-v3', 'cosyvoice-v3-flash'].map(model => ({id:'shared', name:model, model}));
 const draft = {...draftOf({}), name:'歧义测试', voice:'shared'};
 assert.throws(() => rolePayload({}, draft, voices), /音色信息暂不可用/);
 assert.throws(() => rolePayload({}, {...draft, voiceModel:'seed-tts-2.0',rate:0,pitch:0}, voices), /音色信息暂不可用/);
 const saved = rolePayload({}, {...draft, voiceModel:'cosyvoice-v3'}, voices);
 assert.throws(() => rolePayload(saved, {...draftOf(saved), voiceModel:'cosyvoice-v3-flash'}, []), /音色信息暂不可用/);
 assert.deepEqual(rolePayload(saved, {...draftOf(saved),name:'只改名称'}, []).service_config, saved.service_config);
});

test('all TTS provider transitions isolate credentials while preserving the independently configured LLM', () => {
 const models = ['cosyvoice-v3', 'cosyvoice-v3-flash', 'seed-tts-2.0'];
 const voices = models.map(model => ({id:'shared', name:model, model}));
 for (const from of models) for (const to of models) {
  const initial = rolePayload({}, {...draftOf({}),name:'凭据隔离测试',voice:'shared',voiceModel:from,rate:from==='seed-tts-2.0'?0:1,pitch:from==='seed-tts-2.0'?0:1,model:'doubao-seed-2-0-mini-260428'}, voices);
  const oldTTS = initial.service_config!.config!.tts!;
  oldTTS.provider_params!.api_key = 'synthetic-test-key';
  const saved = rolePayload(initial, {...draftOf(initial),voiceModel:to,rate:to==='seed-tts-2.0'?0:1,pitch:to==='seed-tts-2.0'?0:1}, voices);
  const sameProvider = (from==='seed-tts-2.0') === (to==='seed-tts-2.0');
  assert.equal(saved.service_config?.config?.tts?.provider_params?.api_key,sameProvider?'synthetic-test-key':undefined);
  assert.deepEqual(saved.service_config?.config?.llm,initial.service_config?.config?.llm);
  assert.equal(oldTTS.provider_params?.model,from,'saving must not mutate the source role');
 }
});
