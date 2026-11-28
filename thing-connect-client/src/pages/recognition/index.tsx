import { Switch, Text, View } from '@tarojs/components';
import { useState } from 'react';
import { RecognitionReminders } from '../../components/recognition-reminders';
import { Action, Notice } from '../../components/ui';
import { useDeviceWorkspace } from '../../hooks/device-workspace';
import { ApiError, request } from '../../services/api';
import { devicesChanged } from '../../services/device-events';
import { moveScheme, recognitionPayload, type RecognitionConfig, type Scheme } from '../../domain/recognition';

export default function Recognition() {
  const [conflict, setConflict] = useState(false), [notice, setNotice] = useState('');
  const w = useDeviceWorkspace(async id => {
    const [config, catalog] = await Promise.all([request<RecognitionConfig>(url(id)), request<Scheme[]>('/v1/recognition/types')]);
    if (w.alive.current) { setConflict(false); setNotice(''); }
    return { config, catalog };
  });
  function url(id: string) { return '/v1/recognition/devices/' + encodeURIComponent(id) + '/config'; }
  const selected = w.value?.config.schemes ?? [];
  const enabled = !!w.value?.config.policy.enabled;
  const locked = w.busy || conflict;
  async function save(nextEnabled: boolean, schemes: Scheme[]) {
    if (!w.value || !w.device) return;
    let payload;
    try { payload = recognitionPayload(nextEnabled, schemes, w.value.config.policy.revision); }
    catch (e) { w.setError(e instanceof Error ? e.message : '请检查识别设置'); return; }
    await w.run(async () => {
      setNotice('');
      try {
        const config = await request<RecognitionConfig>(url(w.device!.device_id), 'PUT', payload);
        devicesChanged();
        w.commit({ ...w.value!, config });
        if (w.alive.current) setNotice('已自动保存');
      } catch (e) { if (w.alive.current && e instanceof ApiError && e.code === 40901) setConflict(true); throw e; }
    });
  }
  return <View className='page content-page'><View className='row between'><Text className='title device-page-name'>{w.device?.device_name || '当前设备'}</Text><Action disabled={w.busy} onClick={() => void w.refresh()}>重新加载</Action></View><Text className='device-page-description'>为这台设备选择音频识别类型并调整执行顺序，识别结果可返回文字或语音。修改后自动保存。</Text><Notice error>{w.error}</Notice><Notice>{w.busy ? '正在处理…' : notice}</Notice>
    {w.value && <><View className='card'><View className='row between'><Text>开启 AI 识别</Text><Switch checked={enabled} disabled={locked || (!enabled && !selected.length)} color='#07c160' onChange={e => void save(e.detail.value, selected)} /></View>{!enabled && !selected.length && <Text className='muted'>先添加识别类型，即可开启。</Text>}<View className='row between recognition-usage'>
        <Text>本月识别用量</Text>
        <Text className='recognition-usage-value'>{w.value.config.usage.used} / {w.value.config.policy.monthly_limit} 次</Text>
      </View>
      <Text className='muted'>每执行一个识别类型计一次，按 UTC 自然月统计。</Text></View>
      {selected.map((item, index) => <View className='card' key={item.scheme_id}><Text className='label'>{index + 1}. {item.name}</Text><Text className='muted'>{({ text: '文字', audio: '语音', text_audio: '文字＋语音' } as Record<string,string>)[item.output_mode] || item.output_mode}{item.description ? ' · ' + item.description : ''}</Text><View className='row'><Action disabled={locked || index === 0} onClick={() => void save(enabled, moveScheme(selected, index, -1))}>上移</Action><Action disabled={locked || index === selected.length - 1} onClick={() => void save(enabled, moveScheme(selected, index, 1))}>下移</Action><Action danger disabled={locked || (enabled && selected.length === 1)} onClick={() => void save(enabled, selected.filter(s => s.scheme_id !== item.scheme_id))}>移除</Action></View></View>)}
      <View className='card'><Text className='label'>添加识别类型</Text>{w.value.catalog.filter(item => !selected.some(s => s.scheme_id === item.scheme_id)).map(item => <View className='contact row between' key={item.scheme_id}><Text className='grow'>{item.name}</Text><Action disabled={locked || selected.length >= 8} onClick={() => void save(enabled, [...selected, item])}>添加</Action></View>)}{!w.value.catalog.length && <Text className='muted'>暂无可用类型</Text>}<Text className='muted'>已选 {selected.length} / 8 个类型</Text></View>
      {conflict && <Notice error>设备配置已更新，请重新加载后再修改。</Notice>}
      {process.env.TARO_ENV === 'weapp' && w.device && <RecognitionReminders key={w.device.device_id} device={w.device.device_id} enabled={w.value.config.policy.enabled} />}
    </>}
  </View>;
}
