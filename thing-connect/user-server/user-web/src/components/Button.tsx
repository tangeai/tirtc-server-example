import { Button as AntButton } from 'antd';
import type { ButtonHTMLAttributes } from 'react';

// Keep native form semantics while moving existing actions to Ant Design.
export function Button({ type = 'submit', className = '', ...props }: Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'color'>) {
  const classes = className.split(/\s+/);
  const primary = classes.includes('primary');
  const danger = classes.includes('danger');
  return <AntButton {...props} htmlType={type} type={primary ? 'primary' : 'default'} danger={danger}
    loading={props['aria-busy'] === true} block={classes.includes('wide')}
    className={classes.filter(name => !['primary', 'danger', 'wide'].includes(name)).join(' ')}/>;
}
