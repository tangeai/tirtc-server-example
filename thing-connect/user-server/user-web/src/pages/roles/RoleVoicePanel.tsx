import { useEffect, useRef, useState } from 'react';
import { useConfirm } from '../../components/Confirm';
import { useLanguage } from '../../lib/i18n';
import { useRemote } from '../../lib/use-remote';
import { editableProviders, matchingVoices, type Role, type RoleDraft, type Voice } from '../../lib/roles';
import { availableRoleTypes, roleTypePatch, selectedRoleType, type RoleType } from '../../lib/role-types';
import { RoleTypePicker } from './RoleTypePicker';

// One persona panel for creation and editing. Opening it never applies defaults.
export function RoleVoicePanel({ role, draft, patch, voices, voiceLoading, voiceError, retryVoices, busy, active, readonly }: {
  role: Role; draft: RoleDraft; patch(value: Partial<RoleDraft>): void; voices: Voice[];
  voiceLoading: boolean; voiceError: boolean; retryVoices(): void; busy: boolean; active: boolean; readonly: boolean;
}) {
  const { t } = useLanguage(), ask = useConfirm();
  const types = useRemote<{ items: RoleType[] }>(readonly ? undefined : '/v1/ai/role-types');
  const [snapshot, setSnapshot] = useState<RoleType>(), [choosing, setChoosing] = useState(false);
  const [playing, setPlaying] = useState(false), [audioError, setAudioError] = useState(false);
  const live = useRef(true), selection = useRef(false), playIntent = useRef(0), audio = useRef<HTMLAudioElement>(null);
  const provider = editableProviders(role).tts, voice = voices.find(item => item.id === draft.voice);
  const items = types.data?.items || [], available = availableRoleTypes(items, voices);
  const chosen = snapshot?.id === draft.typeId ? snapshot : selectedRoleType(draft, items);
  const isSeed = (draft.voiceModel || voice?.model) === 'seed-tts-2.0';
  const custom = draft.customPrompt || !chosen || draft.prompt !== chosen.prompt;
  const disabled = busy || choosing || readonly;
  function stop() { playIntent.current++; audio.current?.pause(); setPlaying(false); }
  useEffect(() => { live.current = true; return () => { live.current = false; playIntent.current++; audio.current?.pause(); }; }, []);
  useEffect(() => { stop(); setAudioError(false); }, [draft.voice, active]);
  async function choose(item: RoleType) {
    if (selection.current || disabled || !provider) return;
    if (chosen?.id === item.id && draft.voice === item.voice_id) return;
    selection.current = true; setChoosing(true); stop();
    try {
      const edited = !!draft.prompt && (!chosen || custom);
      const decision = edited ? await ask({ title: '保留你写的角色设定？', message: '选择新类型会更换声音，你可以保留当前设定，也可以使用新类型的默认设定。', choices: [{ value: 'cancel', label: '取消' }, { value: 'keep', label: '保留我的设定' }, { value: 'replace', label: '使用默认设定' }] }) : 'replace';
      if (!live.current || decision === 'cancel') return;
      patch(roleTypePatch(draft, item, chosen, decision === 'keep')); setSnapshot(item);
    } finally { selection.current = false; if (live.current) setChoosing(false); }
  }
  async function reset() {
    if (!chosen || disabled) return;
    if (draft.prompt !== chosen.prompt && await ask({ title: '恢复默认设定', message: '当前提示词将替换为所选类型的默认设定。', choices: [{ value: 'cancel', label: '取消' }, { value: 'restore', label: '恢复默认' }] }) !== 'restore') return;
    if (live.current) patch({ prompt: chosen.prompt, customPrompt: false, typeId: chosen.id, typeRevision: chosen.revision });
  }
  async function preview() {
    if (!audio.current || !voice?.sample_url) return;
    if (playing) { stop(); return; }
    stop(); const intent = playIntent.current; setAudioError(false);
    try { await audio.current.play(); if (live.current && intent === playIntent.current) setPlaying(true); }
    catch { if (live.current && intent === playIntent.current) setAudioError(true); }
  }
  return <>
    <section className="role-persona">
      {!readonly && active && <>
        {types.error ? <p className="error">{t('角色类型暂不可用，仍可手动编辑。')} <button type="button" onClick={types.reload}>{t('重试')}</button></p> : types.loading ? <p role="status">{t('正在加载角色类型…')}</p> : <RoleTypePicker items={available} voices={voices} selected={chosen} currentVoiceName={voice?.name || draft.voice} disabled={disabled || voiceLoading || !provider} onOpen={stop} onSelect={choose}/>}
        {!types.loading && !types.error && !available.length && <p className="muted">{t('暂无可用角色类型，可先手动编辑角色和声音。')}</p>}
      </>}
      {!provider && <p className="preset-notice">{t('此角色使用其他语音服务，声音配置保持原样。')}</p>}
      <div className="role-prompt-heading"><label htmlFor="role-prompt">{t('角色设定')}</label><span className="badge">{t(custom ? '自定义设定' : '默认设定')}</span>
        {!readonly && chosen && <label className="radio"><input type="checkbox" role="switch" aria-label={t('自定义提示词')} disabled={disabled} checked={custom} onChange={e => { if (e.target.checked) patch({ customPrompt: true }); else void reset(); }}/>{t('自定义')}</label>}
      </div>
      <textarea id="role-prompt" aria-label={t('角色设定')} className="role-prompt" rows={12} readOnly={readonly || !custom} disabled={busy || choosing} value={draft.prompt} onChange={e => patch({ prompt: e.target.value, customPrompt: true })}/>
      {!readonly && chosen && custom && <button className="role-restore" type="button" disabled={disabled} onClick={() => void reset()}>{t('恢复默认')}</button>}
    </section>
    <details className="role-speech"><summary>{t('语音设置')}</summary>
      <div className="voice-current"><span>{t('当前声音')}：<strong>{voice?.name || draft.voice || t('沿用基础配置')}</strong></span>{voice?.sample_url && <button type="button" disabled={disabled} onClick={() => void preview()}>{t(playing ? '暂停' : '试听')}</button>}</div>
      {voiceError && <p className="error">{t('音色列表暂不可用，请稍后重试')} <button type="button" onClick={retryVoices}>{t('重试')}</button></p>}
      {!readonly && provider && <details><summary>{t('单独调整音色')}</summary><p className="muted">{t('仅更换声音并保留当前提示词；使用预设请在上方选择角色类型。')}</p><label>{t('声音列表')}<select aria-label={t('声音列表')} value={draft.voice} disabled={disabled || voiceLoading} onChange={e => { setSnapshot(undefined); const model = voices.find(v => v.id === e.target.value)?.model || ''; const base = model === 'seed-tts-2.0' ? 0 : 1; patch({ voice: e.target.value, voiceModel: model, rate: base, pitch: base, typeId: '', typeRevision: 0, customPrompt: true }); }}><option value="">{t('沿用基础配置')}</option>{draft.voice && !voices.some(item => item.id === draft.voice) && <option value={draft.voice}>{draft.voice}</option>}{matchingVoices(voices, '', '').map(item => <option value={item.id} key={item.id}>{item.name}</option>)}</select></label><button type="button" onClick={retryVoices}>{t('重新加载')}</button></details>}
      <label>{t('语速')} · {draft.rate}<input type="range" min={isSeed ? -50 : 0.5} max={isSeed ? 100 : 2} step={isSeed ? 1 : 0.1} disabled={disabled || !provider} value={draft.rate} onChange={e => patch({ rate: Number(e.target.value) })}/></label><label>{t('音调')} · {draft.pitch}<input type="range" min={isSeed ? -12 : 0.5} max={isSeed ? 12 : 2} step={isSeed ? 1 : 0.1} disabled={disabled || !provider} value={draft.pitch} onChange={e => patch({ pitch: Number(e.target.value) })}/></label>
      {voice && !voice.sample_url && <p className="muted">{t('此声音暂无试听音频')}</p>}
    </details>
    <audio ref={audio} preload="none" src={voice?.sample_url} onEnded={() => setPlaying(false)} onError={() => { setPlaying(false); setAudioError(true); }}/>{audioError && <p className="error">{t('试听失败，请重试')}</p>}
  </>;
}
