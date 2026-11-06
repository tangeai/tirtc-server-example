export interface ComponentConfig { provider?: string; provider_params?: { model?: string; voice?: string; rate?: number; pitch?: number; [key: string]: unknown }; [key: string]: unknown }
export interface Role {
  id?: string; name?: string; avatar?: string; parent_role_id?: string;
  agent_config?: { prompt?: string; welcome_text?: string; ali_memory?: { enable?: boolean; [key: string]: unknown }; ali_rag?: { index_id?: string; [key: string]: unknown } | null; mcp_tools?: string[]; device_plugins?: string[]; [key: string]: unknown };
  service_config?: { name?: string; type?: string; config?: { llm?: ComponentConfig; tts?: ComponentConfig; [key: string]: unknown }; [key: string]: unknown };
  user_params?: { description?: string; voice?: string; [key: string]: unknown };
  device_plugin_refs?: { id: string; name?: string }[];
}
export interface Voice { id: string; name: string; model: string; languages?: string[]; sample_url?: string }
export interface RoleDraft { name: string; description: string; prompt: string; welcome: string; voice: string; model: string; rate: number; pitch: number; memory: boolean; knowledge: string; mcp: string[]; plugins: string[] }
export function draftOf(role: Role): RoleDraft {
  const tts = role.service_config?.config?.tts?.provider_params;
  return { name: role.name || '', description: role.user_params?.description || '', prompt: role.agent_config?.prompt || '', welcome: role.agent_config?.welcome_text || '',
    voice: tts?.voice || role.user_params?.voice || '', model: role.service_config?.config?.llm?.provider_params?.model || (role.id ? '' : 'qwen3.5-flash'), rate: tts?.rate ?? 1, pitch: tts?.pitch ?? 1,
    memory: role.agent_config?.ali_memory?.enable === true, knowledge: role.agent_config?.ali_rag?.index_id || '', mcp: [...(role.agent_config?.mcp_tools || [])], plugins: [...(role.agent_config?.device_plugins || [])] };
}
export function editableProviders(role: Role) {
  const custom = !role.service_config?.type || role.service_config.type === 'custom-compose';
  const config = role.service_config?.config;
  return { custom, llm: custom && (!config?.llm?.provider || config.llm.provider === 'ali-llm'), tts: custom && (!config?.tts?.provider || config.tts.provider === 'ali-tts') };
}
// Preserve fields this editor does not own, including service credentials and
// inherited providers. Clearing an override is different from a blank provider.
export function rolePayload(previous: Role, draft: RoleDraft, voices: Voice[]): Role {
  const name = draft.name.trim();
  if (!name) throw new Error('请输入智能体名称');
  if (Array.from(name).length > 64) throw new Error('角色名称不能超过 64 个字符');
  const edits = editableProviders(previous), old = previous.service_config?.config || {};
  const voice = voices.find(v => v.id === draft.voice);
  if (edits.tts && draft.voice !== (old.tts?.provider_params?.voice || previous.user_params?.voice || '') && voice?.model && voice.model !== 'cosyvoice-v3-flash') throw new Error('当前音色不适用于阿里语音服务，请重新选择音色');
  const config = { ...old };
  if (edits.llm) {
    if (!draft.model) { if (old.llm?.provider_params?.model) delete config.llm; }
    else if (!previous.service_config || draft.model !== (old.llm?.provider_params?.model || '')) config.llm = { ...old.llm, provider: 'ali-llm', provider_params: { ...old.llm?.provider_params, model: draft.model } };
  }
  if (edits.tts) {
    if (!draft.voice) { if (old.tts?.provider_params?.voice || previous.user_params?.voice) delete config.tts; }
    else if (!previous.service_config || draft.voice !== (old.tts?.provider_params?.voice || previous.user_params?.voice || '') || draft.rate !== (old.tts?.provider_params?.rate ?? 1) || draft.pitch !== (old.tts?.provider_params?.pitch ?? 1)) {
      config.tts = { ...old.tts, provider: 'ali-tts', provider_params: { ...old.tts?.provider_params, model: voice?.model || old.tts?.provider_params?.model || 'cosyvoice-v3-flash', voice: draft.voice, rate: draft.rate, pitch: draft.pitch } };
    }
  }
  return { name, avatar: previous.avatar || '', parent_role_id: previous.parent_role_id || '',
    agent_config: { ...previous.agent_config, prompt: draft.prompt.trim(), welcome_text: draft.welcome.trim(), ali_memory: { ...previous.agent_config?.ali_memory, enable: draft.memory },
      ali_rag: draft.knowledge ? { ...previous.agent_config?.ali_rag, index_id: draft.knowledge } : null, mcp_tools: [...draft.mcp], device_plugins: [...draft.plugins] },
    service_config: edits.custom ? { ...previous.service_config, ...(previous.service_config ? {} : { name: 'default' }), type: 'custom-compose', config } : previous.service_config,
    user_params: { ...previous.user_params, description: draft.description.trim(), voice: draft.voice || undefined } };
}

export function matchingVoices(voices: Voice[], language: string, search: string): Voice[] {
  const query = search.trim().toLowerCase();
  return voices.filter(voice => voice.model === 'cosyvoice-v3-flash' &&
    (!language || voice.languages?.some(value => value.toLowerCase().split(/[-_]/)[0] === language)) &&
    (!query || [voice.id, voice.name].some(value => value.toLowerCase().includes(query))));
}
