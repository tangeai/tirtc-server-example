import { configureVoice, voiceSettings } from './tts-config.ts';
export const llmModels = [
  { id: 'qwen3.5-flash', provider: 'ali-llm' },
  { id: 'qwen3.7-flash', provider: 'ali-llm' },
  { id: 'qwen3.7-plus', provider: 'ali-llm' },
  { id: 'doubao-seed-2-0-mini-260428', provider: 'doubao' },
] as const;

export interface ComponentConfig { provider?: string; provider_params?: { model?: string; audio?: { voice_type?: string; speech_rate?: number; pitch_rate?: number; [key: string]: unknown }; voice?: string; rate?: number; pitch?: number; [key: string]: unknown }; [key: string]: unknown }
export interface Role {
  id?: string; region?: string; name?: string; avatar?: string; parent_role_id?: string;
  agent_config?: { prompt?: string; welcome_text?: string; ali_memory?: { enable?: boolean; [key: string]: unknown }; ali_rag?: { index_id?: string; [key: string]: unknown } | null; mcp_tools?: string[]; device_plugins?: string[]; [key: string]: unknown };
  service_config?: { name?: string; type?: string; config?: { llm?: ComponentConfig; tts?: ComponentConfig; [key: string]: unknown }; [key: string]: unknown };
  user_params?: { description?: string; voice?: string; role_type_id?: string; role_type_revision?: number; role_type_custom?: boolean; [key: string]: unknown };
  device_plugin_refs?: { id: string; name?: string }[];
}
export interface Voice { id: string; name: string; model: string; languages?: string[]; sample_url?: string }
export function findVoice(voices: Voice[], id: string, model?: string): Voice | undefined {
  const matches = voices.filter(voice => voice.id === id && (!model || voice.model === model));
  return matches.length === 1 ? matches[0] : undefined;
}
export interface RoleDraft { region: string; typeId: string; typeRevision: number; voiceModel?: string; customPrompt: boolean | undefined; name: string; description: string; prompt: string; welcome: string; voice: string; model: string; rate: number; pitch: number; memory: boolean; knowledge: string; mcp: string[]; plugins: string[] }
export function draftOf(role: Role): RoleDraft {
  const component = role.service_config?.config?.tts;
  const tts = voiceSettings(component);
  return { region: role.region || (role.id ? '' : 'cn01'), typeId: role.user_params?.role_type_id || '', typeRevision: role.user_params?.role_type_revision || 0, customPrompt: role.user_params?.role_type_custom, name: role.name || '', description: role.user_params?.description || '', prompt: role.agent_config?.prompt || '', welcome: role.agent_config?.welcome_text || '',
    voiceModel: component?.provider_params?.model || '', voice: tts?.voice || role.user_params?.voice || '', model: role.service_config?.config?.llm?.provider_params?.model || (role.id ? '' : 'qwen3.5-flash'), rate: tts?.rate ?? 1, pitch: tts?.pitch ?? 1,
    memory: role.agent_config?.ali_memory?.enable === true, knowledge: role.agent_config?.ali_rag?.index_id || '', mcp: [...(role.agent_config?.mcp_tools || [])], plugins: [...(role.agent_config?.device_plugins || [])] };
}
export function editableProviders(role: Role) {
  const custom = !role.service_config?.type || role.service_config.type === 'custom-compose';
  const config = role.service_config?.config;
  return { custom, llm: custom && (!config?.llm?.provider || config.llm.provider === 'ali-llm' || config.llm.provider === 'doubao'), tts: custom && (!config?.tts?.provider || config.tts.provider === 'ali-tts' || config.tts.provider === 've-seed-tts-2') };
}
// Preserve fields this editor does not own, including service credentials and
// inherited providers. Clearing an override is different from a blank provider.
export function rolePayload(previous: Role, draft: RoleDraft, voices: Voice[]): Role {
  const name = draft.name.trim();
  if (!name) throw new Error('请输入智能体名称');
  if (Array.from(name).length > 64) throw new Error('角色名称不能超过 64 个字符');
  if (!previous.id && !['cn01', 'sg01'].includes(draft.region)) throw new Error('请选择国内或海外地域');
  const edits = editableProviders(previous), old = previous.service_config?.config || {};
  const voice = findVoice(voices, draft.voice, draft.voiceModel || old.tts?.provider_params?.model);
  const previousVoice = voiceSettings(old.tts);
  const previousVoiceID = previousVoice.voice || previous.user_params?.voice || '';
  const config = { ...old };
  if (edits.llm) {
    if (!draft.model) { if (old.llm?.provider_params?.model) delete config.llm; }
    else if (!previous.service_config || draft.model !== (old.llm?.provider_params?.model || '')) {
      const target = llmModels.find(model => model.id === draft.model);
      if (!target) throw new Error('当前模型暂不支持，请重新选择模型');
      const previousProvider = old.llm?.provider || llmModels.find(model => model.id === old.llm?.provider_params?.model)?.provider || 'ali-llm';
      const same = previousProvider === target.provider;
      config.llm = { ...(same ? old.llm : {}), provider: target.provider, provider_params: { ...(same ? old.llm?.provider_params : {}), model: draft.model } };
    }
  }
  if (edits.tts) {
    if (!draft.voice) { if (previousVoiceID) delete config.tts; }
    else if (!previous.service_config || draft.voice !== previousVoiceID || (draft.voiceModel && draft.voiceModel !== old.tts?.provider_params?.model) || draft.rate !== previousVoice.rate || draft.pitch !== previousVoice.pitch) {
      const target = voice || (draft.voice === previousVoiceID ? { id: draft.voice, name: '', model: old.tts?.provider_params?.model || '' } : undefined);
      if (!target || (draft.voiceModel && target.model !== draft.voiceModel)) throw new Error('音色信息暂不可用，请重新加载音色列表');
      config.tts = configureVoice(old.tts, target, draft.rate, draft.pitch);
    }
  }
  return { name, ...(!previous.id ? { region: draft.region } : {}), avatar: previous.avatar || '', parent_role_id: previous.parent_role_id || '',
    agent_config: { ...previous.agent_config, prompt: draft.prompt.trim(), welcome_text: draft.welcome.trim(), ali_memory: { ...previous.agent_config?.ali_memory, enable: draft.memory },
      ali_rag: draft.knowledge ? { ...previous.agent_config?.ali_rag, index_id: draft.knowledge } : null, mcp_tools: [...draft.mcp], device_plugins: [...draft.plugins] },
    service_config: edits.custom ? { ...previous.service_config, ...(previous.service_config ? {} : { name: 'default' }), type: 'custom-compose', config } : previous.service_config,
    user_params: { ...previous.user_params, description: draft.description.trim(), voice: draft.voice || undefined, role_type_id: draft.typeId || undefined, role_type_revision: draft.typeId ? draft.typeRevision : undefined, role_type_custom: draft.typeId ? draft.customPrompt : undefined } };
}

export function roleRegionLabel(region?: string): string {
  return region === 'cn01' ? '国内' : region === 'sg01' ? '海外' : region || '地域未标注';
}
