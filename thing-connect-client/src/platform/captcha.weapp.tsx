import Taro from '@tarojs/taro';
import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react';
import { ApiError } from '../domain/http';
import type { CaptchaConfig, CaptchaHandle, CaptchaProof } from './captcha';
export type { CaptchaConfig, CaptchaHandle, CaptchaProof } from './captcha';
interface NativeCaptcha { popUp?(): void; popup?(): void; show?(): void; reset?(): void }
declare global {
  namespace JSX {
    interface IntrinsicElements {
      'auth-captcha': { id: string; captchaId: string; onVerify(event: { detail: [unknown, string] }): void; onClose(): void };
    }
  }
}

export const Captcha = forwardRef<CaptchaHandle, { config: CaptchaConfig }>(function Captcha({ config }, ref) {
  const pending = useRef<{ resolve(value: CaptchaProof): void; reject(error: Error): void; timer: ReturnType<typeof setTimeout> }>();
  const id = config.public_config?.mini_program_captcha_id || config.public_config?.captcha_id || config.captcha_id || '';
  function reject(message: string) {
    const current = pending.current; pending.current = undefined;
    if (current) { clearTimeout(current.timer); current.reject(new ApiError(-1, message)); }
  }
  useEffect(() => () => reject('验证已取消'), []);
  useImperativeHandle(ref, () => ({ async verify() {
    if (!config.enabled) return undefined;
    if (config.provider !== 'yidun' || !id) throw new ApiError(-1, '当前人机验证服务尚未配置微信小程序入口');
    const page = Taro.getCurrentPages().slice(-1)[0];
    const component = page?.selectComponent?.('#auth-captcha') as NativeCaptcha | undefined;
    const show = component && (component.popUp || component.popup || component.show);
    if (!show) throw new ApiError(-1, '验证控件尚未就绪，请稍后重试');
    reject('验证已重新开始');
    return new Promise<CaptchaProof>((resolve, rejectPromise) => {
      pending.current = { resolve, reject: rejectPromise, timer: setTimeout(() => reject('验证超时，请重新提交'), 120000) };
      // popup creates the plugin's inner instance on its first invocation.
      // Calling reset before that initialization prevents the dialog opening.
      try { show.call(component); } catch { reject('无法打开验证控件，请重新进入页面'); }
    });
  } }), [config, id]);
  return config.enabled && config.provider === 'yidun' && id ? <auth-captcha
    id='auth-captcha' captchaId={id}
    onVerify={(event: { detail: [unknown, string] }) => {
      const [error, value] = event.detail || [];
      if (error || !value) { reject('验证未通过，请重试'); return; }
      const current = pending.current; pending.current = undefined;
      if (current) { clearTimeout(current.timer); current.resolve({ provider: 'yidun', token: value, metadata: { captcha_id: id } }); }
    }}
    onClose={() => reject('验证已取消')}
  /> : null;
});
