import { useEffect, useRef } from 'react';
import { tr } from '../platform/translate';

export function WechatCallGuide({ onClose }: { onClose(): void }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    dialog?.showModal();
    return () => dialog?.close();
  }, []);
  return <dialog ref={ref} className='wechat-call-guide' aria-labelledby='wechat-call-title'
    onCancel={onClose} onClick={event => { if (event.target === ref.current) onClose(); }}>
    <section>
      <h2 id='wechat-call-title'>{tr('在微信中通话')}</h2>
      <p>{tr('用微信扫一扫，打开「探鸽小钛」小程序。')}</p>
      <div className='wechat-call-code'><img src='/static/images/wx-xiaotai.png?v=2' alt={tr('探鸽小钛小程序码')} /></div>
      <p>{tr('也可以在微信搜索「探鸽小钛」，登录同一账号，选择设备后点击「微信通话」。')}</p>
      <button className='wechat-call-close' type='button' onClick={onClose}>{tr('关闭')}</button>
    </section>
  </dialog>;
}
