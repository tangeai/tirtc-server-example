import { useEffect, useRef, useState } from 'react';
import { useConfirm } from '../../components/Confirm';
import { useLanguage } from '../../lib/i18n';
import { useRemote } from '../../lib/use-remote';
import { editableProviders, type Role, type RoleDraft, type Voice } from '../../lib/roles';
import { availableRoleTypes, roleTypePatch, selectedRoleType, type RoleType } from '../../lib/role-types';
import { speechLevelValues, speechLevelIndex } from '../../lib/tts-config';
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
  const voiceModel = draft.voiceModel || voice?.model || '';
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
        {!types.loading && !types.error && !available.length && <p className="muted">{t('暂无可用角色类型，可先编辑角色设定。')}</p>}
      </>}
      {!provider && <p className="preset-notice">{t('此角色使用其他语音服务，声音配置保持原样。')}</p>}
      <div className="role-prompt-heading"><label htmlFor="role-prompt">{t('角色设定')}</label><span className="badge">{t(custom ? '自定义设定' : '默认设定')}</span>
        {!readonly && chosen && <label className="radio"><input type="checkbox" role="switch" aria-label={t('自定义提示词')} disabled={disabled} checked={custom} onChange={e => { if (e.target.checked) patch({ customPrompt: true }); else void reset(); }}/>{t('自定义')}</label>}

      {!readonly && chosen && custom && <button className="role-restore" type="button" disabled={disabled} onClick={() => void reset()}>{t('恢复默认')}</button>}
      </div>
      <textarea id="role-prompt" aria-label={t('角色设定')} className="role-prompt" rows={12} readOnly={readonly || !custom} disabled={busy || choosing} value={draft.prompt} onChange={e => patch({ prompt: e.target.value, customPrompt: true })}/>
    </section>
    <details className="role-speech"><summary>{t('语音设置')}</summary>
      <div className="voice-current"><span>{t('当前声音')}：<strong>{voice?.name || draft.voice || t('沿用基础配置')}</strong></span>{voice?.sample_url ? <button type="button" disabled={disabled} onClick={() => void preview()}>{t(playing ? '暂停' : '试听')}</button> : voice && <span className="muted">{t('暂无试听')}</span>}</div>
      {voiceError && <p className="error">{t('音色列表暂不可用，请稍后重试')} <button type="button" onClick={retryVoices}>{t('重试')}</button></p>}
      <div className="role-speech-controls">
        <SpeechLevel label={t('回复速度')} labels={[t('慢'), t('标准'), t('快')]} values={speechLevelValues(voiceModel, 'rate')} value={draft.rate} disabled={disabled || !provider} onChange={rate => patch({ rate })}/>
        <SpeechLevel label={t('回复音调')} labels={[t('低'), t('标准'), t('高')]} values={speechLevelValues(voiceModel, 'pitch')} value={draft.pitch} disabled={disabled || !provider} onChange={pitch => patch({ pitch })}/>
      </div>
    </details>
    <audio ref={audio} preload="none" src={voice?.sample_url} onEnded={() => setPlaying(false)} onError={() => { setPlaying(false); setAudioError(true); }}/>{audioError && <p className="error">{t('试听失败，请重试')}</p>}
  </>;
}

function SpeechLevel({ label, labels, values, value, disabled, onChange }: {
  label: string; labels: readonly [string, string, string]; values: readonly [number, number, number];
  value: number; disabled: boolean; onChange(value: number): void;
}) {
  const level = speechLevelIndex(value, values);
  return <label className="role-speech-control"><span>{label}</span>
    <input type="range" aria-label={label} aria-valuetext={labels[level]} min="0" max="2" step="1" value={level} disabled={disabled} onChange={event => onChange(values[Number(event.target.value)])}/>
    <span className="role-speech-scale" aria-hidden="true">{labels.map(text => <span key={text}>{text}</span>)}</span>
  </label>;
}
