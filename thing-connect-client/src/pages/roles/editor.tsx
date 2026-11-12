import Taro, { useDidHide } from '@tarojs/taro';
import { Button, Checkbox, CheckboxGroup, Picker, Slider, Switch, Text, Textarea, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Action, Field, Notice, SearchField, Sheet } from '../../components/ui';
import { request, errorText } from '../../services/api';
import { resetWelcomeMode, editableProviders, findVoice, llmModels, roleRegionLabel, type Role, type RoleDraft, type Voice } from '../../domain/roles';
import { availableRoleTypes, roleTypePatch, selectedRoleType, type RoleType } from '../../domain/role-types';
import { speechLevelIndex, speechLevelValues } from '../../domain/tts-config';

export function RoleEditor({ role, draft, voices, busy, patch }: {
  role: Role & { preset?: boolean }; draft: RoleDraft; voices: Voice[]; busy: boolean;
  patch(value: Partial<RoleDraft>): void;
}) {
  const [section, setSection] = useState('basic'), [types, setTypes] = useState<RoleType[]>([]), [loading, setLoading] = useState(false), [error, setError] = useState('');
  const [picker, setPicker] = useState(false), [query, setQuery] = useState(''), [speech, setSpeech] = useState(false), [playing, setPlaying] = useState(''), [previewError, setPreviewError] = useState('');
  const alive = useRef(true), choosing = useRef(false), reading = useRef(false), player = useRef<ReturnType<typeof Taro.createInnerAudioContext>>();
  const providers = editableProviders(role), readonly = !!role.preset, disabled = busy || readonly;
  const chosen = selectedRoleType(draft, types), voice = findVoice(voices, draft.voice, draft.voiceModel);
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
    const result = await Taro.showModal({ title: '恢复默认设定', content: '当前角色设定将替换为所选类型的默认设定。', confirmText: '恢复默认' });
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
        if (alive.current) setPreviewError('试听失败，请重试');
      });
      audio.src = sample.sample_url;
      setPlaying(key);
      audio.play();
    } catch {
      stop(); setPreviewError('当前环境无法试听，请在手机微信中重试');
    }
  }
  return <>
    {readonly && <Notice>这是系统预设，只能查看。需要修改时请新建自己的角色。</Notice>}
    <View className='tabs'>{[['basic', '角色'], ['model', '模型与记忆'], ['advanced', '扩展能力']].map(([id, label]) => <Action key={id} primary={section === id} disabled={busy} onClick={() => setSection(id)}>{label}</Action>)}</View>
    <Notice error>{error}</Notice>{!picker && <Notice error>{previewError}</Notice>}
    {section === 'basic' && <View className='card role-form'>
      <View className='role-identity'><Field label='智能体名称' value={draft.name} disabled={disabled} onChange={name => patch({ name })} />
      <View className='field role-region'><Text className='label'>地域</Text><Picker range={['国内', '海外']} value={draft.region === 'sg01' ? 1 : 0} disabled={disabled || !!role.id} onChange={e => patch({ region: Number(e.detail.value) ? 'sg01' : 'cn01' })}><View className='input role-select'><Text className='role-select-value'>{roleRegionLabel(draft.region)}</Text>{!role.id && <View className='role-select-arrow' />}</View></Picker></View></View>{role.id && <Text className='muted'>地域创建后不可更改，其他地域请新建角色。</Text>}
      {!readonly && <View className='field'><Text className='label'>角色类型</Text><Button className='action role-select' disabled={disabled || loading || !providers.tts} onClick={() => { stop(); setPreviewError(''); setQuery(''); setPicker(true); }}><Text className='role-select-value'>{loading ? '正在加载类型…' : chosen?.name || '选择角色类型'}</Text><View className='role-select-arrow' /></Button>{error && <Action onClick={() => void loadTypes()}>重新加载类型</Action>}</View>}
      <View className='row between'><Text className='label'>角色设定</Text><Text className='badge'>{readonly ? '只读' : custom ? '自定义设定' : '默认设定'}</Text>{chosen && !readonly && <View className='row'><Text>自定义</Text><Switch color={readonly ? '#b8bfbb' : '#07c160'} checked={!!custom} disabled={disabled} onChange={e => { if (e.detail.value) patch({ customPrompt: true }); else void restore(); }} /></View>}</View>
      <Textarea className='textarea' maxlength={-1} value={draft.prompt} disabled={disabled || !custom} placeholder='选择角色类型带入默认设定，或填写自定义角色设定' onInput={e => patch({ prompt: e.detail.value, customPrompt: true })} />
      {chosen && custom && !readonly && <Action disabled={busy} onClick={() => void restore()}>恢复默认设定</Action>}
      <View className='role-speech-heading' hoverClass='role-speech-hover' ariaRole='button' ariaLabel={speech ? '收起语音设置' : '展开语音设置'} onClick={() => { stop(); setSpeech(!speech); }}><View className={'role-speech-chevron' + (speech ? ' is-open' : '')} /><Text>语音设置</Text></View>
      {speech && <><View className='row between'><Text>当前声音：{voice?.name || draft.voice || '沿用基础配置'}</Text>{voice?.sample_url && <Action disabled={busy} onClick={() => preview(voice, 'current')}>{playing === 'current' ? '停止' : '试听'}</Action>}</View>{(['rate', 'pitch'] as const).map(setting => {
        const levels = speechLevelValues(draft.voiceModel || voice?.model || '', setting), labels = setting === 'rate' ? ['慢', '标准', '快'] : ['低', '标准', '高'];
        return <View className='field' key={setting}><Text className='label'>{setting === 'rate' ? '回复速度' : '回复音调'}</Text><Slider className='role-speech-slider' min={0} max={2} step={1} value={speechLevelIndex(draft[setting], levels)} disabled={disabled || !providers.tts} activeColor={disabled || !providers.tts ? '#b8bfbb' : '#07c160'} backgroundColor='#e5e9e7' blockSize={20} onChange={e => patch({ [setting]: levels[e.detail.value] })} /><View className='role-speech-scale'>{labels.map(label => <Text key={label}>{label}</Text>)}</View></View>;
      })}</>}
    </View>}
    {section === 'model' && <View className='card'><Text className='title'>模型与记忆</Text><Picker range={['沿用基础配置', ...llmModels.map(m => m.id)]} value={Math.max(0, llmModels.findIndex(m => m.id === draft.model) + 1)} disabled={disabled || !providers.llm} onChange={e => patch({ model: Number(e.detail.value) ? llmModels[Number(e.detail.value) - 1].id : '' })}><View className='input role-select'><Text className='role-select-value'>{draft.model || '沿用基础配置'}</Text>{!disabled && providers.llm && <View className='role-select-arrow' />}</View></Picker><View className='row between'><Text>长期记忆</Text><Switch color={readonly ? '#b8bfbb' : '#07c160'} checked={draft.memory} disabled={disabled} onChange={e => patch({ memory: e.detail.value })} /></View></View>}
    {section === 'advanced' && <View className='card'><View className='row between'><Text>视频识别</Text><Switch color={readonly ? '#b8bfbb' : '#07c160'} checked={draft.video === true} disabled={disabled || !providers.custom} onChange={e => patch({ video: e.detail.value })} /></View><Text className='muted'>允许 AI 理解设备上传的视频画面。{draft.video === undefined ? '未单独设置，沿用基础配置。' : ''}</Text><View className='field'><Text className='label'>{draft.welcomeMode === 'ai' ? 'AI 欢迎语提示词' : '开场白'}</Text><Textarea className='textarea' maxlength={-1} value={draft.welcomeMode === 'ai' ? draft.welcomePrompt : draft.welcome} disabled={disabled} onInput={e => patch(draft.welcomeMode === 'ai' ? { welcomePrompt: e.detail.value } : { welcome: e.detail.value })} />
      <CheckboxGroup onChange={e => patch(resetWelcomeMode(e.detail.value.includes('ai')))}><Checkbox value='ai' checked={draft.welcomeMode === 'ai'} disabled={disabled} color='#07c160'>AI 随机欢迎语</Checkbox></CheckboxGroup>
      <Text className='muted'>勾选后，输入内容作为 AI 提示词；不勾选时，直接使用输入的文字。勾选或取消勾选都会重置为默认内容。</Text><Text className='muted'>{draft.welcomeMode === 'ai' ? '提示词必填，每次新会话由 AI 生成欢迎语，内容可能重复。' : '留空并保存会清除本角色的欢迎语设置。'}</Text></View><Text className='muted'>知识库、MCP 和设备插件的管理入口暂在 Web；已有配置会保留。</Text></View>}
    {picker && <Sheet title='选择角色类型' busy={busy} onClose={() => { stop(); setPreviewError(''); setPicker(false); }}><SearchField placeholder='搜索角色类型或声音' value={query} onChange={value => { stop(); setQuery(value); }} /><Notice error>{previewError}</Notice>{available.map(item => {
      const sample = findVoice(voices, item.voice_id, item.voice_model);
      return <View className='role-type-option' key={item.id}><Button className={'action role-type-name' + (chosen?.id === item.id ? ' role-type-selected' : '')} disabled={disabled} onClick={() => void choose(item)}>{item.name}</Button><Button className='action role-type-preview' disabled={busy || !sample?.sample_url} onClick={() => preview(sample, item.id)}>{!sample?.sample_url ? '暂无试听' : playing === item.id ? '停止' : '试听'}</Button></View>;
    })}{!available.length && <Notice>暂无匹配的可用角色类型，可直接编辑角色设定。</Notice>}</Sheet>}
  </>;
}
