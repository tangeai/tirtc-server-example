import { Button, Input, Text, View } from '@tarojs/components';
import type { PropsWithChildren } from 'react';

export function Action({ children, onClick, disabled, primary, danger }: PropsWithChildren<{ onClick(): void; disabled?: boolean; primary?: boolean; danger?: boolean }>) {
  return <Button className={`action ${primary ? 'primary' : ''} ${danger ? 'danger' : ''}`} disabled={disabled} onClick={onClick}>{children}</Button>;
}
export function Field({ label, value, onChange, password, numeric, disabled, placeholder }: { label: string; value: string; onChange(value: string): void; password?: boolean; numeric?: boolean; disabled?: boolean; placeholder?: string }) {
  return <View className='field'><Text className='label'>{label}</Text><Input className='input field-input' value={value} password={password} type={numeric ? 'number' : 'text'} disabled={disabled} placeholder={placeholder || label} onInput={e => onChange(e.detail.value)} /></View>;
}
export function Notice({ children, error }: PropsWithChildren<{ error?: boolean }>) { return children ? <View className={error ? 'notice error' : 'notice'}>{children}</View> : null; }
export function Sheet({ title, onClose, children, busy }: PropsWithChildren<{ title: string; onClose(): void; busy?: boolean }>) {
  return <View className='mask' onClick={() => { if (!busy) onClose(); }}><View className='sheet' onClick={e => e.stopPropagation()}><Text className='title'>{title}</Text>{children}<Action disabled={busy} onClick={onClose}>关闭</Action></View></View>;
}

export function SearchField({ value, onChange, placeholder }: { value: string; onChange(value: string): void; placeholder: string }) {
  return <View className='search-bar'><View className='search-symbol' ariaHidden /><Input className='search-bar-input' value={value} placeholder={placeholder} ariaLabel={placeholder} confirmType='search' onInput={e => onChange(e.detail.value)} />{value && <Button className='search-clear' ariaLabel='清空搜索' onClick={() => onChange('')}>清除</Button>}</View>;
}

export function RobotIcon() {
  return <View className='device-robot' ariaHidden><View className='robot-antenna' /><View className='robot-ear robot-ear-left' /><View className='robot-face'><View className='robot-eyes'><View className='robot-eye' /><View className='robot-eye' /></View><View className='robot-smile' /></View><View className='robot-ear robot-ear-right' /></View>;
}
