import { Modal } from 'antd';
import { useEffect, useState, type ReactNode } from 'react';
import { useLanguage } from '../lib/i18n';

export function Dialog({ title, children, onClose, size, closeDisabled = false }: { title: string; children: ReactNode; onClose(): void; size?: 'wide' | 'history' | 'workspace' | 'compact' | 'info'; closeDisabled?: boolean }) {
  const { t } = useLanguage();
  // Callers unmount completed forms immediately; restore their trigger even when
  // there is no closing animation. Modal owns trapping, Escape and scroll lock.
  const [trigger] = useState(() => document.activeElement);
  useEffect(() => {
    return () => { if (trigger instanceof HTMLElement && trigger.isConnected) trigger.focus(); };
  }, [trigger]);
  return <Modal open centered title={<h2>{t(title)}</h2>} footer={null}
    width={size === 'wide' ? 960 : size === 'history' ? 1080 : (size === 'workspace' || size === 'info') ? 640 : 480}
    styles={{ header: { borderBottom: '1px solid #e8ecea', paddingBottom: 12, marginBottom: 16 } }}
    transitionName="" maskTransitionName=""
    rootClassName="app-modal" className={'app-dialog' + (size ? ' app-dialog-' + size : '')}
    closable={{ disabled: closeDisabled, 'aria-label': t('关闭弹窗') }} keyboard={!closeDisabled} maskClosable={!closeDisabled}
    onCancel={event => { event.stopPropagation(); if (!closeDisabled) onClose(); }} destroyOnHidden>
    {children}
  </Modal>;
}
