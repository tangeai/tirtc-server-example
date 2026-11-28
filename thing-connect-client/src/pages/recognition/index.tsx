import { tr, usePageLanguage } from '../../platform/translate';
import Taro from '@tarojs/taro';
import { Text, View } from '@tarojs/components';
import { useState } from 'react';
import { RecognitionReminders } from '../../components/recognition-reminders';
import { Action, Notice } from '../../components/ui';
import { useDeviceWorkspace } from '../../hooks/device-workspace';
import { ApiError, errorText, request } from '../../services/api';
import { devicesChanged } from '../../services/device-events';
import { RecognitionTypes } from '../../components/recognition-types';
import { recognitionPayload, type RecognitionConfig, type Scheme } from '../../domain/recognition';

export default function Recognition() {
  usePageLanguage();
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
      } catch (e) {
        if (w.alive.current) {
          if (e instanceof ApiError && e.code === 40901) setConflict(true);
          void Taro.showToast({ title: errorText(e), icon: 'none' });
        }
        throw e;
      }
    });
  }
  return <View className='page content-page settings-page'>
    <View className='row'><Text className='title device-page-name'>{w.device?.device_name || tr("当前设备")}</Text></View>
    <Notice error>{w.error}</Notice>
    {!w.value && w.error && <Action disabled={w.busy} onClick={() => void w.refresh()}>{tr("重新加载识别设置")}</Action>}
    {w.busy && !w.value && <Notice>{tr("正在加载识别设置…")}</Notice>}
    {w.value && <><View className='settings-section'>
      <RecognitionTypes selected={selected} catalog={w.value.catalog} locked={locked}
        change={schemes => void save(schemes)} />
      <Text className='settings-footnote'>{tr("开启任一类型即可识别，全部关闭即停止。修改后自动保存。")}</Text>
    </View>
      {conflict && <View className='card'>
        <Notice error>{tr("设备配置已更新，载入最新设置后可继续修改。")}</Notice>
        <Action disabled={w.busy} onClick={() => void w.refresh()}>{tr("载入最新设置")}</Action>
      </View>}
      {w.device && <RecognitionReminders key={w.device.device_id} device={w.device.device_id} enabled={w.value.config.policy.enabled} />}
      <View className='settings-section'><View className='card'><View className='row between'>
        <Text>{tr("本月识别用量")}</Text><Text className='recognition-usage-value'>{w.value.config.usage.used} / {w.value.config.policy.monthly_limit} {tr("次")}</Text>
      </View></View><Text className='settings-footnote'>{tr("每执行一个识别类型计一次，按 UTC 自然月统计。")}</Text></View>
    </>}
  </View>;
}
