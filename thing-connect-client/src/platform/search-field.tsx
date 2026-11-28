import { Input, View } from '@tarojs/components';
import { Button } from './button';
import { tr } from './translate';

export function SearchField({ value, onChange, placeholder }: { value: string; onChange(value: string): void; placeholder: string }) {
  return <View className='search-bar'><View className='search-symbol' ariaHidden /><Input className='search-bar-input' value={value} placeholder={placeholder} ariaLabel={placeholder} confirmType='search' onInput={e => onChange(e.detail.value)} />{value && <Button className='search-clear' ariaLabel={tr("清空搜索")} onClick={() => onChange('')}>{tr("清除")}</Button>}</View>;
}
