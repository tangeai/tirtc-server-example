import { useState } from 'react';
import { Text, View } from '@tarojs/components';
import type { Device } from '../domain/devices';
import { deviceRows, mediaRows } from '../../../thing-connect/user-server/user-web/src/lib/devices';
import { tr } from '../platform/translate';
import { Action, Sheet } from './ui';

export function DeviceInfo({ device, onClose }: { device: Device; onClose(): void }) {
  const [scene, setScene] = useState<'stream' | 'call' | 'voip'>('stream');
  const [raw, setRaw] = useState(false);
  const info = { ...device, profiles: device.profiles && Object.fromEntries(Object.entries(device.profiles).map(([key, value]) => [key, { ...value }])) };
  const rows = mediaRows(info, scene);
  function fields(items: [string, string][]) {
    return items.map(([name, value]) => <View className='device-info-row' key={name}><Text className='muted'>{name}</Text><Text selectable>{value}</Text></View>);
  }
  return <Sheet title={tr('设备信息')} onClose={onClose}>
    {fields(deviceRows(info))}
    <View className='tabs'>{([['stream', '实时查看'], ['call', '设备通话'], ['voip', '微信通话']] as const).map(([key, label]) =>
      <Action key={key} primary={key === scene} onClick={() => { setScene(key); setRaw(false); }}>{tr(label)}</Action>)}</View>
    {rows ? <>{fields(rows)}
      <Action onClick={() => setRaw(!raw)}>{tr(raw ? '收起' : '查看原始媒体参数')}</Action>
      {raw && <Text className='device-info-json' selectable>{JSON.stringify(device.profiles?.[scene], null, 2)}</Text>}
    </> : <Text className='muted'>{tr('该场景能力未上报')}</Text>}
  </Sheet>;
}
