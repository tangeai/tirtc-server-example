import { Text, View } from '@tarojs/components';
import { useState } from 'react';
import { RecognitionReminders } from '../../components/recognition-reminders';
import { Action, Notice } from '../../components/ui';
import { useDeviceWorkspace } from '../../hooks/device-workspace';
import { ApiError, request } from '../../services/api';
import { devicesChanged } from '../../services/device-events';
import { RecognitionTypes } from '../../components/recognition-types';
import { recognitionPayload, type RecognitionConfig, type Scheme } from '../../domain/recognition';

export default function Recognition() {
  const [conflict, setConflict] = useState(false);
  const w = useDeviceWorkspace(async id => {
    const [config, catalog] = await Promise.all([request<RecognitionConfig>(url(id)), request<Scheme[]>('/v1/recognition/types')]);
    if (w.alive.current) setConflict(false);
    return { config, catalog };
  });
  function url(id: string) { return '/v1/recognition/devices/' + encodeURIComponent(id) + '/config'; }
  const selected = w.value?.config.policy.enabled ? w.value.config.schemes : [];
  const locked = w.busy || conflict;
  async function save(schemes: Scheme[]) {
    if (!w.value || !w.device) return;
    let payload;
    try { payload = recognitionPayload(schemes.length > 0, schemes, w.value.config.policy.revision); }
    catch (e) { w.setError(e instanceof Error ? e.message : '请检查识别设置'); return; }
    await w.run(async () => {
      try {
        const config = await request<RecognitionConfig>(url(w.device!.device_id), 'PUT', payload);
        devicesChanged();
        w.commit({ ...w.value!, config });
      } catch (e) { if (w.alive.current && e instanceof ApiError && e.code === 40901) setConflict(true); throw e; }
    });
  }
  return <View className='page content-page'><View className='row between'><Text className='title device-page-name'>{w.device?.device_name || '当前设备'}</Text><Action disabled={w.busy} onClick={() => void w.refresh()}>重新加载</Action></View><Text className='device-page-description'>打开任一类型即可开启 AI 识别，全部关闭即停止。修改后自动保存。</Text><Notice error>{w.error}</Notice>{w.busy && !w.value && <Notice>正在加载识别设置…</Notice>}
    {w.value && <><View className='card'><View className='row between'>
        <Text>本月识别用量</Text>
        <Text className='recognition-usage-value'>{w.value.config.usage.used} / {w.value.config.policy.monthly_limit} 次</Text>
      </View>
      <Text className='muted'>每执行一个识别类型计一次，按 UTC 自然月统计。</Text></View>
      <RecognitionTypes selected={selected} catalog={w.value.catalog} locked={locked}
        change={schemes => void save(schemes)} />
      {conflict && <Notice error>设备配置已更新，请重新加载后再修改。</Notice>}
      {process.env.TARO_ENV === 'weapp' && w.device && <RecognitionReminders key={w.device.device_id} device={w.device.device_id} enabled={w.value.config.policy.enabled} />}
    </>}
  </View>;
}
