import { useEffect, useId, useRef, type ReactNode } from 'react';
import { useLanguage } from '../lib/i18n';

// Each open mounts a new dialog. A late request from an unmounted dialog cannot
// change the state of another device's dialog.
export function Dialog({ title, children, onClose, size, closeDisabled = false }: { title: string; children: ReactNode; onClose(): void; size?: 'wide'; closeDisabled?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  const id = useId();
  const { t } = useLanguage();
  useEffect(() => {
    const dialog = ref.current!;
    const previous = document.activeElement;
    dialog.showModal();
    return () => { dialog.close(); if (previous instanceof HTMLElement && previous.isConnected) previous.focus(); };
  }, []);
  return <dialog ref={ref} className={size === 'wide' ? 'dialog-wide' : undefined} aria-labelledby={id} onCancel={event => { event.preventDefault(); if (!closeDisabled) onClose(); }} onClick={event => { if (!closeDisabled && event.target === event.currentTarget) onClose(); }}>
    <div className="dialog-body"><header><h2 id={id}>{t(title)}</h2><button className="close" type="button" disabled={closeDisabled} aria-label={t('关闭弹窗')} onClick={onClose}>×</button></header>{children}</div>
  </dialog>;
}
