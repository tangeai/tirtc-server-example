import { tr } from '../platform/translate';
import { Button } from '../platform/button';
import { Text, View } from '@tarojs/components';
import { useState } from 'react';
import type { Device } from '../domain/devices';
import { capabilityRows } from '../domain/device-info';
import { Sheet } from './ui';

export function DeviceInfo({ device, onClose }: { device: Device; onClose(): void }) {
  const [raw, setRaw] = useState(false);
  return <Sheet title={tr("设备信息")} onClose={onClose}>
    <View className='device-info-block'><Text className='device-name'>{device.device_name || tr("未命名设备")}</Text><View className='device-info-row'><Text className='muted'>{tr("状态")}</Text><Text className={device.online ? 'online' : 'offline'}>{device.online ? tr("在线") : tr("离线")}</Text></View><View className='device-info-row'><Text className='muted'>{tr("设备 ID")}</Text><Text className='device-info-value' selectable>{device.device_id}</Text></View></View>
    {([['MAC', device.mac], ['芯片型号', device.hardware?.chip_model], ['开发板型号', device.hardware?.board_model], ['固件版本', device.firmware_version]] as const).map(([label, value]) => <View className='device-info-row' key={label}><Text>{label}</Text><Text selectable>{value || tr("未上报")}</Text></View>)}
    <Text className='muted'>{tr("以下为设备最近上报的音视频能力。设备离线时无法发起实时查看或通话。")}</Text>
    {(['stream', 'voip', 'call'] as const).map(scene => <View className='device-info-block' key={scene}><Text className='label'>{scene === 'stream' ? tr("实时查看") : scene === 'voip' ? tr("微信通话") : tr("设备间通话")}</Text>{device.profiles?.[scene] ? capabilityRows(device.profiles[scene]).map(row => <View className='device-info-row' key={row.label}><Text className='muted'>{row.label}</Text><Text>{row.value}</Text></View>) : <Text className='muted'>{tr("尚未上报，暂时无法判断该功能的能力。")}</Text>}</View>)}
    <Button className='device-raw-toggle' onClick={() => setRaw(!raw)}><Text>{tr("原始信息")}</Text><Text className='muted'>{raw ? tr("收起 ∧") : tr("展开 ∨")}</Text></Button>
    {raw && <View className='device-info-raw'><Text className='muted'>{tr("用于技术排查，保留设备返回的原始字段。")}</Text><Text className='device-info-json' selectable>{JSON.stringify(device, null, 2)}</Text></View>}
  </Sheet>;
}
