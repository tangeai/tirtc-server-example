import { useEffect, useId, useRef, type ReactNode } from 'react';
import { useLanguage } from '../lib/i18n';

// Each open mounts a new dialog. A late request from an unmounted dialog cannot
// change the state of another device's dialog.
export function Dialog({ title, children, onClose, size, closeDisabled = false }: { title: string; children: ReactNode; onClose(): void; size?: 'wide' | 'history' | 'workspace' | 'media' | 'compact'; closeDisabled?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  const id = useId();
  const { t } = useLanguage();
  useEffect(() => {
    const dialog = ref.current!;
    const previous = document.activeElement;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    dialog.showModal();
    return () => { dialog.close(); document.body.style.overflow = overflow; if (previous instanceof HTMLElement && previous.isConnected) previous.focus(); };
  }, []);
  return <dialog ref={ref} className={size ? 'dialog-' + size : undefined} aria-labelledby={id} onCancel={event => { event.preventDefault(); event.stopPropagation(); if (!closeDisabled) onClose(); }} onClick={event => { if (!closeDisabled && event.target === event.currentTarget) { const bounds = event.currentTarget.getBoundingClientRect(); if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) onClose(); } }}>
    <div className="dialog-body"><header><h2 id={id}>{t(title)}</h2><button className="close" type="button" disabled={closeDisabled} aria-label={t('关闭弹窗')} onClick={onClose}>×</button></header>{children}</div>
  </dialog>;
}
