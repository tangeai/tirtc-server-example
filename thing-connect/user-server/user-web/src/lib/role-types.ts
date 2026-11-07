import type { RoleDraft, Voice } from './roles.ts';
export interface RoleType { id: string; name: string; voice_id: string; voice_model: string; prompt: string; sort_order: number; revision: number }
export function availableRoleTypes(items: RoleType[], voices: Voice[]) {
  return items.filter(item => voices.some(voice => voice.id === item.voice_id && voice.model === item.voice_model && voice.model === 'cosyvoice-v3-flash'));
}
// A template is copied into a draft once. Catalog refreshes never reapply it.
export function roleTypePatch(draft: RoleDraft, next: RoleType, previous: RoleType | undefined, keepPrompt: boolean): Partial<RoleDraft> {
  return { voice: next.voice_id, prompt: keepPrompt ? draft.prompt : next.prompt,
    ...(!draft.name.trim() || draft.name === previous?.name ? { name: next.name } : {}) };
}
