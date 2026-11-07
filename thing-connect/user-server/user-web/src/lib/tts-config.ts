import type { ComponentConfig, Voice } from './roles.ts';

export function supportedVoiceModel(model: string): boolean {
  return model === 'cosyvoice-v3-flash' || model === 'seed-tts-2.0';
}
export function seedTTS(tts?: ComponentConfig): boolean { return tts?.provider === 've-seed-tts-2'; }
export function voiceSettings(tts?: ComponentConfig) {
  const params = tts?.provider_params;
  return seedTTS(tts)
    ? { voice: params?.audio?.voice_type || '', rate: params?.audio?.speech_rate ?? 0, pitch: params?.audio?.pitch_rate ?? 0 }
    : { voice: params?.voice || '', rate: params?.rate ?? 1, pitch: params?.pitch ?? 1 };
}
// Keep provider-specific fields on same-provider edits; never carry credentials
// or model options into another provider's configuration.
export function configureVoice(old: ComponentConfig | undefined, voice: Voice, rate: number, pitch: number): ComponentConfig {
  if (!supportedVoiceModel(voice.model)) throw new Error('当前音色模型暂不支持，请重新选择音色');
  const seed = voice.model === 'seed-tts-2.0', provider = seed ? 've-seed-tts-2' : 'ali-tts';
  const same = !old?.provider || old.provider === provider;
  const previous = same ? old?.provider_params : undefined;
  if (seed) {
    if (!Number.isInteger(rate) || !Number.isInteger(pitch) || rate < -50 || rate > 100 || pitch < -12 || pitch > 12) throw new Error('语速应为 -50 至 100、音调应为 -12 至 12 的整数');
    return { ...(same ? old : {}), provider, provider_params: { ...previous, model: voice.model, audio: { ...previous?.audio, voice_type: voice.id, speech_rate: rate, pitch_rate: pitch } } };
  }
  if (!Number.isFinite(rate) || !Number.isFinite(pitch) || rate < 0.5 || rate > 2 || pitch < 0.5 || pitch > 2) throw new Error('语速和音调应为 0.5 至 2');
  return { ...(same ? old : {}), provider, provider_params: { ...previous, model: voice.model, voice: voice.id, rate, pitch } };
}
