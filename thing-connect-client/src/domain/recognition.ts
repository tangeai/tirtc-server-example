export interface Scheme { scheme_id: string; scheme_version: number; name: string; description?: string; output_mode: string; limits?: { max_audio_seconds: number } }
export interface RecognitionConfig { policy: { enabled: boolean; revision: number; monthly_limit: number }; schemes: Scheme[]; usage: { used: number } }
export function recognitionPayload(enabled: boolean, selected: Scheme[], revision: number) {
  if (selected.length > 8) throw new Error('最多选择 8 个识别类型');
  if (enabled && !selected.length) throw new Error('请至少选择一个识别类型');
  if (new Set(selected.map(s => s.scheme_id)).size !== selected.length) throw new Error('识别类型不能重复');
  return { enabled, schemes: selected.map(({ scheme_id, scheme_version }) => ({ scheme_id, scheme_version })), expected_revision: revision };
}
export function moveScheme(items: Scheme[], index: number, offset: number) {
  const target = index + offset;
  if (index < 0 || index >= items.length || target < 0 || target >= items.length) return items;
  const result = [...items]; const [item] = result.splice(index, 1); result.splice(target, 0, item); return result;
}
