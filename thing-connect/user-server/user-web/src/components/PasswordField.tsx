import { useId, useState, type InputHTMLAttributes } from 'react';
import { useLanguage } from '../lib/i18n';

type Props = Omit<InputHTMLAttributes<HTMLInputElement>, 'type' | 'id'> & { label: string };
export function PasswordField({ label, ...input }: Props) {
  const { t } = useLanguage();
  const id = useId(), [visible, setVisible] = useState(false);
  const action = t(visible ? '隐藏{p0}' : '显示{p0}', { p0: t(label) });
  return <div className="password-field"><label htmlFor={id}>{t(label)}</label><div className="password-control">
    <input {...input} id={id} type={visible ? 'text' : 'password'}/>
    <button type="button" className="password-toggle" disabled={input.disabled} aria-label={action} title={action} aria-controls={id} aria-pressed={visible} onClick={() => setVisible(value => !value)}>
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>{visible && <path d="m3 3 18 18"/>}</svg>
    </button>
  </div></div>;
}
