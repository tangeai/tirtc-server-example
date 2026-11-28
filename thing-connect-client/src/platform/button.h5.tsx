import { Button as TaroButton } from '@tarojs/components';
import type { ComponentProps, KeyboardEvent } from 'react';

// Keep pointer events in Taro's event system, including inside modal sheets.
export function Button(props: ComponentProps<typeof TaroButton>) {
  const keyboard = {
    role: 'button',
    tabIndex: props.disabled ? -1 : 0,
    'aria-disabled': Boolean(props.disabled),
    onKeyDown(event: KeyboardEvent<HTMLElement>) {
      if (props.disabled || event.repeat || (event.key !== 'Enter' && event.key !== ' ')) return;
      event.preventDefault();
      event.currentTarget.click();
    },
  };
  return <TaroButton {...props} {...keyboard} />;
}
