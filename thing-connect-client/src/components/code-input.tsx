import { Input, Text, View } from '@tarojs/components';
import { useState } from 'react';

// One native input keeps paste, backspace and the numeric keyboard consistent.
// The six visual cells do not introduce six separate focus/keyboard lifecycles.
export function CodeInput({ value, onChange, disabled, label = '6 位验证码' }: {
  value: string; onChange(value: string): void; disabled?: boolean; label?: string;
}) {
  const [focused, setFocused] = useState(false);
  return <View className={'code-input' + (disabled ? ' is-disabled' : '')}>
    <View className='code-cells' ariaHidden>
      {Array.from({ length: 6 }, (_, index) => <View key={index} className={'code-cell' + (focused && index === Math.min(value.length, 5) ? ' is-active' : '')}><Text>{value[index] || ''}</Text></View>)}
    </View>
    <Input className='code-native-input' ariaLabel={label} type='number' maxlength={6} value={value} disabled={disabled}
      onFocus={() => setFocused(true)} onBlur={() => setFocused(false)}
      onInput={event => onChange(event.detail.value.normalize('NFKC').replace(/\D/g, '').slice(0, 6))} />
  </View>;
}
