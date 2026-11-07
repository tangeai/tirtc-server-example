import { supportedVoiceModel } from './tts-config.ts';
import type { RoleDraft, Voice } from './roles.ts';
export interface RoleType { id: string; name: string; voice_id: string; voice_model: string; prompt: string; sort_order: number; revision: number }
export function availableRoleTypes(items: RoleType[], voices: Voice[]) {
  return items.filter(item => voices.some(voice => voice.id === item.voice_id && voice.model === item.voice_model && supportedVoiceModel(voice.model)));
}
// A template is copied into a draft once. Catalog refreshes never reapply it.
export function roleTypePatch(draft: RoleDraft, next: RoleType, previous: RoleType | undefined, keepPrompt: boolean): Partial<RoleDraft> {
  return { typeId: next.id, typeRevision: next.revision, customPrompt: keepPrompt, voice: next.voice_id, voiceModel: next.voice_model, ...(draft.voiceModel !== next.voice_model ? { rate: next.voice_model === 'seed-tts-2.0' ? 0 : 1, pitch: next.voice_model === 'seed-tts-2.0' ? 0 : 1 } : {}), prompt: keepPrompt ? draft.prompt : next.prompt,
    ...(!draft.name.trim() || draft.name === previous?.name ? { name: next.name } : {}) };
}

// Legacy roles are matched only when both voice and prompt agree. Shared voices
// cannot identify a type, and catalog refreshes must not rewrite saved prompts.
export function selectedRoleType(draft: RoleDraft, items: RoleType[]): RoleType | undefined {
  if (draft.typeId) return items.find(item => item.id === draft.typeId);
  const matches = items.filter(item => item.voice_id === draft.voice && (!draft.voiceModel || item.voice_model === draft.voiceModel) && item.prompt === draft.prompt);
  return matches.length === 1 ? matches[0] : undefined;
}
