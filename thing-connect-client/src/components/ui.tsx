import { tr } from '../platform/translate';
import { Button } from '../platform/button';
import { Input, Text, View } from '@tarojs/components';
import type { PropsWithChildren } from 'react';

export function Action({ children, onClick, disabled, primary, danger, muted, ariaLabel }: PropsWithChildren<{ onClick(): void; disabled?: boolean; primary?: boolean; danger?: boolean; muted?: boolean; ariaLabel?: string }>) {
  return <Button className={`action ${primary ? 'primary' : ''} ${danger ? 'danger' : ''} ${muted ? 'is-unavailable' : ''}`} ariaLabel={ariaLabel} disabled={disabled} onClick={onClick}>{children}</Button>;
}
export function DevicePageHeading({ title, device }: { title: string; device?: { device_name?: string; device_id: string } }) {
  const name = device?.device_name?.trim() || device?.device_id;
  if (process.env.TARO_ENV !== 'h5') return <Text className='title device-page-name'>{name || tr('当前设备')}</Text>;
  return <View className='device-page-heading'><Text className='title'>{tr(title)}</Text>
    {name && <Text className='device-page-context'>{tr('当前设备')} · {name}</Text>}
  </View>;
}

export function Field({ label, value, onChange, password, numeric, disabled, placeholder, focus = false }: { label: string; value: string; onChange(value: string): void; password?: boolean; numeric?: boolean; disabled?: boolean; placeholder?: string; focus?: boolean }) {
  return <View className='field'><Text className='label'>{label}</Text><Input className='input field-input' value={value} password={password} type={numeric ? 'number' : 'text'} disabled={disabled} focus={focus} placeholder={placeholder || label} onInput={e => onChange(e.detail.value)} /></View>;
}
export function Notice({ children, error }: PropsWithChildren<{ error?: boolean }>) { return children ? <View className={error ? 'notice error' : 'notice'}>{typeof children === 'string' ? tr(children) : children}</View> : null; }
export function Sheet({ title, onClose, children, busy }: PropsWithChildren<{ title: string; onClose(): void; busy?: boolean }>) {
  return <View className='mask' onClick={() => { if (!busy) onClose(); }}><View className='sheet' onClick={e => e.stopPropagation()}><Text className='title'>{title}</Text>{children}<Action disabled={busy} onClick={onClose}>{tr("关闭")}</Action></View></View>;
}

export function SearchField({ value, onChange, placeholder }: { value: string; onChange(value: string): void; placeholder: string }) {
  return <View className='search-bar'><View className='search-symbol' ariaHidden /><Input className='search-bar-input' value={value} placeholder={placeholder} ariaLabel={placeholder} confirmType='search' onInput={e => onChange(e.detail.value)} />{value && <Button className='search-clear' ariaLabel={tr("清空搜索")} onClick={() => onChange('')}>{tr("清除")}</Button>}</View>;
}

export function RobotIcon() {
  return <View className='device-robot' ariaHidden><View className='robot-antenna' /><View className='robot-ear robot-ear-left' /><View className='robot-face'><View className='robot-eyes'><View className='robot-eye' /><View className='robot-eye' /></View><View className='robot-smile' /></View><View className='robot-ear robot-ear-right' /></View>;
}
