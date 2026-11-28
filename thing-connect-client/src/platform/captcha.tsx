import { CaptchaGate } from '../../../thing-connect/user-server/user-web/src/lib/captcha';
import { forwardRef, useEffect, useRef, useImperativeHandle } from 'react';
import { ApiError } from '../domain/http';
export interface CaptchaConfig { enabled: boolean; provider: string; public_config?: { mini_program_captcha_id?: string; captcha_id?: string }; captcha_id?: string }
export interface CaptchaProof { provider: string; token: string; metadata: { captcha_id: string } }
export interface CaptchaHandle { verify(): Promise<CaptchaProof | undefined> }
export const Captcha = forwardRef<CaptchaHandle, { config: CaptchaConfig }>(function Captcha({ config }, ref) {
  const gate = useRef<CaptchaGate>();
  const cancel = useRef<() => void>();
  useEffect(() => {
    gate.current = new CaptchaGate(() => cancel.current?.());
    return () => { gate.current?.dispose(); cancel.current?.(); };
  }, []);
  useImperativeHandle(ref, () => ({ async verify() {
    if (!config.enabled) return undefined;
    const current = gate.current;
    if (!current) throw new ApiError(-1, '验证控件尚未就绪');
    return new Promise<CaptchaProof | undefined>((resolve, reject) => {
      cancel.current = () => reject(new ApiError(-1, '验证已取消，请重新提交'));
      void current.open(result => {
        cancel.current = undefined;
        resolve(result ? { provider: result.provider, token: result.token, metadata: { ...result.metadata, captcha_id: result.captcha_id } } : undefined);
      }).catch(() => reject(new ApiError(-1, '验证控件加载失败，请重试')));
    });
  } }), [config]);
  return null;
});
