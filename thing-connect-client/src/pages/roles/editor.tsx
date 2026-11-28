import { tr } from '../../platform/translate';
import Taro, { useDidHide } from '@tarojs/taro';
import { Button } from '../../platform/button';
import { Checkbox, CheckboxGroup, Picker, Slider, Switch, Text, Textarea, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Action, Field, Notice, SearchField, Sheet } from '../../components/ui';
import { request, errorText } from '../../services/api';
import { resetWelcomeMode, editableProviders, findVoice, llmModels, roleRegionLabel, type Role, type RoleDraft, type Voice } from '../../domain/roles';
import { availableRoleTypes, typeVoiceLabel, typeVoicePatch, roleTypePatch, selectedRoleType, type RoleType } from '../../domain/role-types';
import { speechLevelIndex, speechLevelValues } from '../../domain/tts-config';

import { ConfigDetails } from './config-details';
import { RoleResources } from './resources';

export function RoleEditor({ role, draft, voices, busy, patch }: {
  role: Role & { preset?: boolean }; draft: RoleDraft; voices: Voice[]; busy: boolean;
  patch(value: Partial<RoleDraft>): void;
}) {
  const [section, setSection] = useState('basic'), [types, setTypes] = useState<RoleType[]>([]), [loading, setLoading] = useState(false), [error, setError] = useState('');
  const [picker, setPicker] = useState(false), [query, setQuery] = useState(''), [speech, setSpeech] = useState(false), [playing, setPlaying] = useState(''), [previewError, setPreviewError] = useState('');
  const alive = useRef(true), choosing = useRef(false), reading = useRef(false), player = useRef<ReturnType<typeof Taro.createInnerAudioContext>>();
  const providers = editableProviders(role), readonly = !!role.preset, disabled = busy || readonly;
  const chosen = selectedRoleType(draft, types), voice = findVoice(voices, draft.voice, draft.voiceModel);
  const typeVoice = chosen && findVoice(voices, chosen.voice_id, chosen.voice_model);
  const differentVoice = chosen && (chosen.voice_id !== draft.voice || chosen.voice_model !== (draft.voiceModel || voice?.model));
  const custom = draft.customPrompt || !chosen || draft.prompt !== chosen.prompt;
  const available = availableRoleTypes(types, voices).filter(item => [item.name, findVoice(voices, item.voice_id, item.voice_model)?.name || ''].some(s => s.toLowerCase().includes(query.trim().toLowerCase())));
  function stop() { const audio = player.current; player.current = undefined; audio?.destroy(); if (alive.current) setPlaying(''); }
  useDidHide(stop);
  async function loadTypes() {
    if (reading.current) return;
    reading.current = true; setLoading(true); setError('');
    try { const result = await request<{ items: RoleType[] }>('/v1/ai/role-types'); if (alive.current) setTypes(result.items || []); }
    catch (e) { if (alive.current) setError(errorText(e)); }
    finally { reading.current = false; if (alive.current) setLoading(false); }
  }
  useEffect(() => { alive.current = true; if (!readonly) void loadTypes(); return () => { alive.current = false; player.current?.destroy(); }; }, []);
  useEffect(() => { stop(); }, [section, draft.voice, draft.voiceModel]);
  async function choose(item: RoleType) {
    if (choosing.current || disabled || !providers.tts) return;
    choosing.current = true; stop();
    try {
      let keep = false;
      if (draft.prompt && (!chosen || custom)) {
        const answer = await Taro.showActionSheet({ itemList: ['保留我的设定', '使用新类型的默认设定'] }).catch(() => undefined);
        if (!answer || !alive.current) return;
        keep = answer.tapIndex === 0;
      }
      if (alive.current) { patch(roleTypePatch(draft, item, chosen, keep)); setPicker(false); }
    } finally { choosing.current = false; }
  }
  async function restore() {
    if (!chosen || disabled) return;
    const result = await Taro.showModal({ title: tr("恢复默认设定"), content: tr("当前角色设定将替换为所选类型的默认设定。"), confirmText: tr("恢复默认") });
    if (result.confirm && alive.current) patch({ prompt: chosen.prompt, customPrompt: false, typeId: chosen.id, typeRevision: chosen.revision });
  }
  function preview(sample: Voice | undefined, key: string) {
    if (playing === key) { stop(); return; }
    stop(); setPreviewError('');
    if (!sample?.sample_url) return;
    try {
      const audio = Taro.createInnerAudioContext();
      player.current = audio;
      audio.onEnded(() => { if (player.current === audio) stop(); });
      audio.onError(() => {
        if (player.current !== audio) return;
        stop();
        if (alive.current) setPreviewError(tr("试听失败，请重试"));
      });
      audio.src = sample.sample_url;
      setPlaying(key);
      audio.play();
    } catch {
      stop(); setPreviewError(tr("当前环境无法试听，请在手机微信中重试"));
    }
  }
  return <>
    {readonly && <Notice>{tr("这是系统预设，只能查看。需要修改时请新建自己的角色。")}</Notice>}
    <View className='tabs'>{[['basic', '角色'], ['model', '模型与记忆'], ['advanced', '扩展能力']].map(([id, label]) => <Action key={id} primary={section === id} disabled={busy} onClick={() => setSection(id)}>{tr(label)}</Action>)}</View>
    <Notice error>{error}</Notice>{!picker && <Notice error>{previewError}</Notice>}
    {section === 'basic' && <View className='card role-form'>
      <View className='role-identity'><Field label={tr("智能体名称")} value={draft.name} disabled={disabled} onChange={name => patch({ name })} />
      <View className='field role-region'><Text className='label'>{tr("地域")}</Text><Picker range={[tr("国内"), tr("海外")]} value={draft.region === 'sg01' ? 1 : 0} disabled={disabled || !!role.id} onChange={e => patch({ region: Number(e.detail.value) ? 'sg01' : 'cn01' })}><View className='input role-select'><Text className='role-select-value'>{tr(roleRegionLabel(draft.region))}</Text>{!role.id && <View className='role-select-arrow' />}</View></Picker></View></View>{role.id && <Text className='muted'>{tr("地域不可更改，跨地域请新建角色。")}</Text>}
      <Field label={tr("角色描述")} value={draft.description} disabled={disabled} onChange={description=>patch({description})}/>
      {!readonly && <View className='field'><Text className='label'>{tr("角色类型")}</Text><Button className='action role-select' disabled={disabled || loading || !providers.tts} onClick={() => { stop(); setPreviewError(''); setQuery(''); setPicker(true); }}><Text className='role-select-value'>{loading ? tr("正在加载类型…") : chosen?.name || tr("选择角色类型")}</Text><View className='role-select-arrow' /></Button>{chosen && <Text className='muted'>{typeVoiceLabel(chosen, typeVoice)}</Text>}{differentVoice && <View className='row between'><Text>{tr("当前使用 ·")}{voice?.name || draft.voice || tr("基础配置")}</Text>{typeVoice && <Action disabled={disabled || !providers.tts} onClick={() => patch(typeVoicePatch(draft, chosen!))}>{tr("使用类型音色")}</Action>}</View>}{error && <Action onClick={() => void loadTypes()}>{tr("重新加载类型")}</Action>}</View>}
      <View className='row between role-prompt-heading'><Text className='label'>{tr("角色设定")}</Text>{readonly && <Text className='badge'>{tr("只读")}</Text>}{chosen && custom && !readonly && <Button className='action role-restore' disabled={busy} onClick={() => void restore()}>{tr("恢复默认")}</Button>}{chosen && !readonly && <View className='row role-toggle'><Text className='label'>{tr("自定义")}</Text><Switch color={readonly ? '#b8bfbb' : '#07c160'} checked={!!custom} disabled={disabled} onChange={e => { if (e.detail.value) patch({ customPrompt: true }); else void restore(); }} /></View>}</View>
      <Textarea className='textarea' maxlength={-1} value={draft.prompt} disabled={disabled || !custom} placeholder={tr("选择角色类型带入默认设定，或填写自定义角色设定")} onInput={e => patch({ prompt: e.detail.value, customPrompt: true })} />
      <Button className='role-speech-heading' ariaLabel={speech ? tr("收起语音设置") : tr("展开语音设置")} onClick={() => { stop(); setSpeech(!speech); }}><View className={'role-speech-chevron' + (speech ? ' is-open' : '')} /><Text>{tr("语音设置")}</Text></Button>
      {speech && <><View className='row between'><Text>{tr("当前声音：")}{voice?.name || draft.voice || tr("沿用基础配置")}</Text>{voice?.sample_url && <Action disabled={busy} onClick={() => preview(voice, 'current')}>{playing === 'current' ? tr("停止") : tr("试听")}</Action>}</View>{(['rate', 'pitch'] as const).map(setting => {
        const levels = speechLevelValues(draft.voiceModel || voice?.model || '', setting), labels = setting === 'rate' ? ['慢', '标准', '快'] : ['低', '标准', '高'];
        return <View className='field' key={setting}><Text className='label'>{setting === 'rate' ? tr("回复速度") : tr("回复音调")}</Text><Slider className='role-speech-slider' min={0} max={2} step={1} value={speechLevelIndex(draft[setting], levels)} disabled={disabled || !providers.tts} activeColor={disabled || !providers.tts ? '#b8bfbb' : '#07c160'} backgroundColor='#e5e9e7' blockSize={20} onChange={e => patch({ [setting]: levels[e.detail.value] })} /><View className='role-speech-scale'>{labels.map(label => <Text key={label}>{tr(label)}</Text>)}</View></View>;
      })}</>}
    </View>}
    {section === 'model' && <View className='card role-form'><View className='field'><Text className='label'>{tr("模型")}</Text><Picker range={[tr("沿用基础配置"), ...llmModels.map(m => m.id)]} value={Math.max(0, llmModels.findIndex(m => m.id === draft.model) + 1)} disabled={disabled || !providers.llm} onChange={e => patch({ model: Number(e.detail.value) ? llmModels[Number(e.detail.value) - 1].id : '' })}><View className='input role-select'><Text className='role-select-value'>{draft.model || tr("沿用基础配置")}</Text>{!disabled && providers.llm && <View className='role-select-arrow' />}</View></Picker></View><View className='row between role-toggle'><Text className='label'>{tr("长期记忆")}</Text><Switch color={readonly ? '#b8bfbb' : '#07c160'} checked={draft.memory} disabled={disabled} onChange={e => patch({ memory: e.detail.value })} /></View></View>}
    {section === 'advanced' && <View className='role-extensions'><View className='role-extension-settings'><View className='card role-form'><View className='row between role-toggle'><Text className='label'>{tr("视频识别")}</Text><Switch color={readonly ? '#b8bfbb' : '#07c160'} checked={draft.video === true} disabled={disabled || !providers.custom} onChange={e => patch({ video: e.detail.value })} /></View><Text className='muted'>{tr("允许 AI 理解设备上传的视频画面。")}{draft.video === undefined ? tr("未单独设置，沿用基础配置。") : ''}</Text></View><View className='card role-form'><View className='field role-welcome-field'><Text className='label'>{draft.welcomeMode === 'ai' ? tr("AI 欢迎语提示词") : tr("开场白")}</Text><Textarea className='textarea role-welcome-input' maxlength={-1} value={draft.welcomeMode === 'ai' ? draft.welcomePrompt : draft.welcome} disabled={disabled} onInput={e => patch(draft.welcomeMode === 'ai' ? { welcomePrompt: e.detail.value } : { welcome: e.detail.value })} />
      <CheckboxGroup className='role-welcome-choice' onChange={e => patch(resetWelcomeMode(e.detail.value.includes('ai')))}><Checkbox value='ai' checked={draft.welcomeMode === 'ai'} disabled={disabled} color='#07c160'>{tr("AI 随机欢迎语")}</Checkbox></CheckboxGroup>
      <Text className='muted'>{tr("勾选后，输入内容作为 AI 提示词；不勾选时，直接使用输入的文字。勾选或取消勾选都会重置为默认内容。")}</Text><Text className='muted'>{draft.welcomeMode === 'ai' ? tr("提示词必填，每次新会话由 AI 生成欢迎语，内容可能重复。") : tr("留空并保存会清除本角色的欢迎语设置。")}</Text></View></View></View>
      <View className='role-extension-resources'>
      <RoleResources kind='knowledge' selected={draft.knowledge ? [draft.knowledge] : []} change={ids=>patch({knowledge:ids[0] || ''})} disabled={disabled} readonly={readonly}/>
      <RoleResources kind='mcp' selected={draft.mcp} change={mcp=>patch({mcp})} disabled={disabled} readonly={readonly}/>
      <RoleResources kind='plugins' selected={draft.plugins} names={role.device_plugin_refs} change={plugins=>patch({plugins})} disabled={disabled} readonly={readonly}/>
      {role.id && <ConfigDetails title={tr("已保存的角色配置")} value={{id: role.id, name: role.name, region: role.region, avatar: role.avatar, parent_role_id: role.parent_role_id, agent_config: role.agent_config, service_config: role.service_config, user_params: role.user_params}}/>}
    </View></View>}
    {picker && <Sheet title={tr("选择角色类型")} busy={busy} onClose={() => { stop(); setPreviewError(''); setPicker(false); }}><SearchField placeholder={tr("搜索角色类型或声音")} value={query} onChange={value => { stop(); setQuery(value); }} /><Notice error>{previewError}</Notice>{available.map(item => {
      const sample = findVoice(voices, item.voice_id, item.voice_model);
      return <View className='role-type-option' key={item.id}><Button className={'action role-type-name' + (chosen?.id === item.id ? ' role-type-selected' : '')} disabled={disabled} onClick={() => void choose(item)}><Text>{item.name}</Text><Text className='role-option-voice'>{typeVoiceLabel(item, sample)}</Text></Button><Button className='action role-type-preview' disabled={busy || !sample?.sample_url} onClick={() => preview(sample, item.id)}>{!sample?.sample_url ? tr("暂无试听") : playing === item.id ? tr("停止") : tr("试听")}</Button></View>;
    })}{!available.length && <Notice>{tr("暂无匹配的可用角色类型，可直接编辑角色设定。")}</Notice>}</Sheet>}
  </>;
}
