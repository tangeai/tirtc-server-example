import { forwardRef, useImperativeHandle } from 'react';
import { ApiError } from '../domain/http';
export interface CaptchaConfig { enabled: boolean; provider: string; public_config?: { mini_program_captcha_id?: string; captcha_id?: string }; captcha_id?: string }
export interface CaptchaProof { provider: string; token: string; metadata: { captcha_id: string } }
export interface CaptchaHandle { verify(): Promise<CaptchaProof | undefined> }
export const Captcha = forwardRef<CaptchaHandle, { config: CaptchaConfig }>(function Captcha({ config }, ref) {
  useImperativeHandle(ref, () => ({ async verify() {
    if (!config.enabled) return undefined;
    throw new ApiError(-1, '此构建版本尚未接入浏览器人机验证，请使用微信小程序');
  } }), [config]);
  return null;
});
