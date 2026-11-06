import { request } from './api';
import { LatestIntent } from './latest-intent';

type Metadata = Record<string, unknown>;
export interface CaptchaToken { captcha_id: string; validate: string; provider: string; token: string; metadata: Metadata }
interface Config { enabled: boolean; provider: string; captcha_id?: string; public_config?: Record<string, string> }
interface Instance { verify(): void; refresh(): void; destroy?(): void }
interface Geetest {
  onSuccess(callback: () => void): void;
  getValidate(): { lot_number: string } | null;
  reset(): void; showCaptcha(): void; destroy?(): void;
}
interface Aliyun { show(): void; startTracelessVerification?(): void; destroy?(): void }
declare global {
  interface Window {
    initNECaptcha?: (options: { captchaId: string; element: string; mode: string; width: string; apiVersion: number; onClose(): void; onVerify(error: unknown, data?: { validate: string }): void }, ready: (instance: Instance) => void, fail: (error: unknown) => void) => void;
    initGeetest4?: (options: { captchaId: string; product: string; language: string }, ready: (instance: Geetest) => void) => void;
    TencentCaptcha?: new (id: string, callback: (result: { ret: number; ticket: string; randstr?: string }) => void, options: { userLanguage: string }) => { show(): void; destroy?(): void };
    AliyunCaptchaConfig?: { region: string; prefix: string };
    initAliyunCaptcha?: (options: { SceneId: string; mode: string; element: string; button: string; success(token: string): void; fail(error: unknown): void; onError(error: unknown): void; getInstance(instance: Aliyun): void; slideStyle: { width: number; height: number }; language: string; delayBeforeSuccess: boolean }) => void;
  }
}
const scripts = new Map<string, Promise<void>>();
function loadScript(src: string) {
  let pending = scripts.get(src);
  if (!pending) {
    pending = new Promise<void>((resolve, reject) => {
      const script = document.createElement('script');
      const timeout = setTimeout(() => { script.remove(); scripts.delete(src); reject(new Error('captcha timeout')); }, 10000);
      script.src = src; script.async = true;
      script.onload = () => { clearTimeout(timeout); resolve(); };
      script.onerror = () => { clearTimeout(timeout); script.remove(); scripts.delete(src); reject(new Error('captcha unavailable')); };
      document.head.append(script);
    });
    scripts.set(src, pending);
  }
  return pending;
}

async function createInstance(config: Config, element: string, verified: (token: CaptchaToken) => void, closed: () => void): Promise<Instance> {
  const id = config.public_config?.captcha_id || config.captcha_id || '';
  if (!id) throw new Error('captcha configuration missing');
  const token = (value: string, metadata: Metadata = {}) => verified({ captcha_id: id, validate: value,
    provider: config.provider, token: value, metadata: { captcha_id: id, ...metadata } });
  const urls: Record<string, string> = { yidun: 'https://cstaticdun.126.net/load.min.js', geetest: 'https://static.geetest.com/v4/gt4.js',
    tencent: 'https://turing.captcha.qcloud.com/TJCaptcha.js', aliyun: 'https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js' };
  if (!urls[config.provider]) throw new Error('unsupported captcha');
  if (config.provider === 'aliyun') {
    const prefix = config.public_config?.prefix;
    if (!prefix) throw new Error('captcha configuration missing');
    window.AliyunCaptchaConfig = { prefix, region: config.public_config?.region || 'cn' };
  }
  await loadScript(urls[config.provider]);
  return new Promise<Instance>((resolve, reject) => {
    let expired = false;
    const timeout = setTimeout(() => { expired = true; reject(new Error('captcha initialization timeout')); }, 10000);
    const ready = (instance: Instance) => { clearTimeout(timeout); if (expired) instance.destroy?.(); else resolve(instance); };
    const fail = () => { clearTimeout(timeout); expired = true; reject(new Error('captcha initialization failed')); };
    try {
      switch (config.provider) {
        case 'yidun':
          if (!window.initNECaptcha) return fail();
          window.initNECaptcha({ captchaId: id, element, mode: 'popup', width: Math.min(320, innerWidth - 24) + 'px', apiVersion: 2, onClose: closed,
            onVerify: (error, data) => { if (!expired && !error && data?.validate) token(data.validate); } }, ready, fail);
          break;
        case 'geetest':
          if (!window.initGeetest4) return fail();
          window.initGeetest4({ captchaId: id, product: 'bind', language: 'zho' }, instance => {
            instance.onSuccess(() => { const data = instance.getValidate(); if (!expired && data) token(data.lot_number, data); });
            ready({ verify: () => instance.showCaptcha(), refresh: () => instance.reset(), destroy: () => instance.destroy?.() });
          });
          break;
        case 'tencent': {
          if (!window.TencentCaptcha) return fail();
          const instance = new window.TencentCaptcha(id, result => { if (!expired && result.ret === 0 && result.ticket) token(result.ticket, { randstr: result.randstr || '' }); else if (!expired && result.ret === 2) closed(); }, { userLanguage: 'zh-cn' });
          ready({ verify: () => instance.show(), refresh() {}, destroy: () => instance.destroy?.() });
          break;
        }
        case 'aliyun':
          if (!window.initAliyunCaptcha) return fail();
          window.initAliyunCaptcha({ SceneId: id, mode: 'popup', element, button: element,
            success: value => { if (!expired) token(value, { captcha_verify_param: value }); }, fail() {}, onError: fail,
            getInstance: instance => ready({ verify: () => { instance.show(); instance.startTracelessVerification?.(); }, refresh() {}, destroy: () => instance.destroy?.() }),
            slideStyle: { width: Math.min(360, innerWidth - 24), height: 40 }, language: 'cn', delayBeforeSuccess: false });
          break;
      }
    } catch { fail(); }
  });
}

export class CaptchaGate {
  private config?: Config;
  private instance?: Instance;
  private active?: (token: CaptchaToken | null) => void;
  private disposed = false;
  private readonly closed: () => void;
  constructor(closed: () => void = () => {}) { this.closed = closed; }
  private intent = new LatestIntent(async () => {
    this.config ??= await request<Config>('/v1/config/captcha', { anonymous: true });
    if (!this.config.enabled) return null;
    this.instance ??= await createInstance(this.config, '#captcha-mount', token => {
      const action = this.active; this.active = undefined;
      action?.(token);
    }, () => { if (!this.disposed && this.active) { this.cancel(); this.closed(); } });
    if (this.disposed) { this.instance.destroy?.(); return null; }
    return this.instance;
  });
  cancel() { this.active = undefined; this.intent.cancel(); }
  async open(action: (token: CaptchaToken | null) => void) {
    this.active = undefined;
    await this.intent.run(instance => {
      if (this.disposed) return;
      if (instance) { this.active = action; instance.verify(); }
      else action(null);
    });
  }
  reset() { this.instance?.refresh(); }
  dispose() { this.disposed = true; this.cancel(); this.instance?.destroy?.(); }
}
export function captchaFields(token: CaptchaToken | null) {
  return { captcha_id: token?.captcha_id ?? '', validate: token?.validate ?? '', user: '',
    captcha: token ? { provider: token.provider, token: token.token, metadata: token.metadata } : null };
}
