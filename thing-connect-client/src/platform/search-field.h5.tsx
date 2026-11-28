import { useRef } from 'react';
import { tr } from './translate';

export function SearchField({ value, onChange, placeholder }: { value: string; onChange(value: string): void; placeholder: string }) {
  const input = useRef<HTMLInputElement>(null);
  return <div className='search-bar web-search'>
    <label className='web-search-label'>
      <span className='search-symbol' aria-hidden='true' />
      <input className='web-search-input' ref={input} type='text' inputMode='search' enterKeyHint='search' aria-label={placeholder}
        placeholder={placeholder} value={value} onChange={event => onChange(event.currentTarget.value)} />
    </label>
    {value && <button type='button' className='search-clear' aria-label={tr('清空搜索')} onClick={() => {
      onChange('');
      input.current?.focus();
    }}>{tr('清除')}</button>}
  </div>;
}
