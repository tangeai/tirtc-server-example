import { Button, Text, View } from '@tarojs/components';
import { useState } from 'react';
import type { Device } from '../domain/devices';
import { capabilityRows } from '../domain/device-info';
import { Sheet } from './ui';

export function DeviceInfo({ device, onClose }: { device: Device; onClose(): void }) {
  const [raw, setRaw] = useState(false);
  return <Sheet title='设备信息' onClose={onClose}>
    <View className='device-info-block'><Text className='device-name'>{device.device_name || '未命名设备'}</Text><View className='device-info-row'><Text className='muted'>状态</Text><Text className={device.online ? 'online' : 'offline'}>{device.online ? '在线' : '离线'}</Text></View><View className='device-info-row'><Text className='muted'>设备 ID</Text><Text className='device-info-value' selectable>{device.device_id}</Text></View></View>
    <Text className='muted'>以下为设备最近上报的音视频能力。设备离线时无法发起实时查看或通话。</Text>
    {(['stream', 'voip', 'call'] as const).map(scene => <View className='device-info-block' key={scene}><Text className='label'>{scene === 'stream' ? '实时查看' : scene === 'voip' ? '微信通话' : '设备间通话'}</Text>{device.profiles?.[scene] ? capabilityRows(device.profiles[scene]).map(row => <View className='device-info-row' key={row.label}><Text className='muted'>{row.label}</Text><Text>{row.value}</Text></View>) : <Text className='muted'>尚未上报，暂时无法判断该功能的能力。</Text>}</View>)}
    <Button className='device-raw-toggle' onClick={() => setRaw(!raw)}><Text>原始信息</Text><Text className='muted'>{raw ? '收起 ∧' : '展开 ∨'}</Text></Button>
    {raw && <View className='device-info-raw'><Text className='muted'>用于技术排查，保留设备返回的原始字段。</Text><Text className='device-info-json' selectable>{JSON.stringify(device, null, 2)}</Text></View>}
  </Sheet>;
}
