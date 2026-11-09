import { Switch, Text, View } from '@tarojs/components';
import { useState } from 'react';
import { Action, Notice } from '../../components/ui';
import { useDeviceWorkspace } from '../../hooks/device-workspace';
import { ApiError, request } from '../../services/api';
import { devicesChanged } from '../../services/device-events';
import { moveScheme, recognitionPayload, type RecognitionConfig, type Scheme } from '../../domain/recognition';

export default function Recognition() {
  const [selected, setSelected] = useState<Scheme[]>([]), [enabled, setEnabled] = useState(false), [conflict, setConflict] = useState(false), [notice, setNotice] = useState('');
  const w = useDeviceWorkspace(async id => {
    const [config, catalog] = await Promise.all([request<RecognitionConfig>(url(id)), request<Scheme[]>('/v1/recognition/types')]);
    if (w.alive.current) { setSelected(config.schemes); setEnabled(config.policy.enabled); setConflict(false); setNotice(''); }
    return { config, catalog };
  });
  function url(id: string) { return '/v1/recognition/devices/' + encodeURIComponent(id) + '/config'; }
  async function save() {
    if (!w.value || !w.device) return;
    let payload;
    try { payload = recognitionPayload(enabled, selected, w.value.config.policy.revision); }
    catch (e) { w.setError(e instanceof Error ? e.message : '请检查识别设置'); return; }
    await w.run(async () => {
      setNotice('');
      try {
        const config = await request<RecognitionConfig>(url(w.device!.device_id), 'PUT', payload);
        devicesChanged();
        w.commit({ ...w.value!, config });
        if (w.alive.current) { setSelected(config.schemes); setEnabled(config.policy.enabled); setConflict(false); setNotice('设置已保存'); }
      } catch (e) { if (w.alive.current && e instanceof ApiError && e.code === 40901) setConflict(true); throw e; }
    });
  }
  return <View className='page content-page'><View className='row between'><Text className='title device-page-name'>{w.device?.device_name || '当前设备'}</Text><Action disabled={w.busy} onClick={() => void w.refresh()}>重新加载</Action></View><Text className='device-page-description'>为这台设备选择音频识别类型并调整执行顺序，识别结果可返回文字或语音。修改后请保存设置。</Text><Notice error>{w.error}</Notice><Notice>{w.busy ? '正在处理…' : notice}</Notice>
    {w.value && <><View className='card'><View className='row between'><Text>开启 AI 识别</Text><Switch checked={enabled} disabled={w.busy} color='#07c160' onChange={e => setEnabled(e.detail.value)} /></View><Text className='muted'>同一段音频按下方顺序分析，保存后生效。</Text></View>
      {selected.map((item, index) => <View className='card' key={item.scheme_id}><Text className='label'>{index + 1}. {item.name}</Text><Text className='muted'>{({ text: '文字', audio: '语音', text_audio: '文字＋语音' } as Record<string,string>)[item.output_mode] || item.output_mode}{item.description ? ' · ' + item.description : ''}</Text><View className='row'><Action disabled={w.busy || index === 0} onClick={() => setSelected(moveScheme(selected, index, -1))}>上移</Action><Action disabled={w.busy || index === selected.length - 1} onClick={() => setSelected(moveScheme(selected, index, 1))}>下移</Action><Action danger disabled={w.busy} onClick={() => setSelected(selected.filter(s => s.scheme_id !== item.scheme_id))}>移除</Action></View></View>)}
      <View className='card'><Text className='label'>添加识别类型</Text>{w.value.catalog.filter(item => !selected.some(s => s.scheme_id === item.scheme_id)).map(item => <View className='contact row between' key={item.scheme_id}><Text className='grow'>{item.name}</Text><Action disabled={w.busy || selected.length >= 8} onClick={() => setSelected([...selected, item])}>添加</Action></View>)}{!w.value.catalog.length && <Text className='muted'>暂无可用类型</Text>}<Text className='muted'>已选 {selected.length} / 8 个类型</Text></View>
      <Text className='muted'>本月已占用 {w.value.config.usage.used} / {w.value.config.policy.monthly_limit} 次，每个类型计一次。</Text>
      {conflict && <Notice error>配置已变化，草稿已保留。请核对后重新加载，再保存；不会自动覆盖其他端的修改。</Notice>}
      <View className='wide'><Action primary disabled={w.busy || conflict} onClick={() => void save()}>保存设置</Action></View></>}
  </View>;
}
