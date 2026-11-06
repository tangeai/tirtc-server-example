import { useEffect, useRef, useState, type FormEvent } from 'react';
import { Shell } from '../components/Shell';
import { PasswordField } from '../components/PasswordField';
import { ApiError, clearSession, errorText, request } from '../lib/api';
import { CaptchaGate, captchaFields, type CaptchaToken } from '../lib/captcha';
import { useLanguage } from '../lib/i18n';

type Mode = 'login' | 'register' | 'reset';
type AuthValues = { email: string; password: string; confirm: string; code: string };
const pathFor = (mode: Mode) => mode === 'reset' ? '/forgot-password' : '/' + mode;
function initialMode(): Mode {
  return location.pathname === '/forgot-password' ? 'reset' : location.pathname === '/register' || new URLSearchParams(location.search).get('tab') === 'register' ? 'register' : 'login';
}
function LegalLinks() {
  const { t } = useLanguage();
  return <><a href="/static/legal/terms.html" target="_blank" rel="noopener noreferrer">{t('《用户协议》')}</a> {t('和')} <a href="/static/legal/privacy.html" target="_blank" rel="noopener noreferrer">{t('《隐私政策》')}</a></>;
}
export default function Auth() {
  const [mode, setMode] = useState<Mode>(initialMode);
  const { t } = useLanguage();
  function switchMode(next: Mode) { setMode(next); history.replaceState(null, '', pathFor(next)); }
  useEffect(() => { const pop = () => setMode(initialMode()); window.addEventListener('popstate', pop); return () => window.removeEventListener('popstate', pop); }, []);
  useEffect(() => { if (mode !== 'reset' && localStorage.getItem('token')) location.replace('/devices'); }, [mode]);
  const title = mode === 'login' ? '登录账号' : mode === 'register' ? '注册账号' : '找回密码';
  const backToDevices = mode === 'reset' && !!localStorage.getItem('token');
  return <Shell title="小钛控制台" back={backToDevices ? '/devices' : mode === 'reset' ? '/login' : '/'} backLabel={backToDevices ? '返回设备列表' : mode === 'reset' ? '返回登录' : '返回首页'}>
    <main className="auth-layout"><section className="card auth-card"><h1>{t(title)}</h1>
      {mode === 'reset' ? <p className="muted">{t('验证注册邮箱后即可设置新密码')}</p> : <nav className="tabs" aria-label={t('账号操作')}><button type="button" aria-pressed={mode === 'login'} onClick={() => switchMode('login')}>{t('登录')}</button><button type="button" aria-pressed={mode === 'register'} onClick={() => switchMode('register')}>{t('注册')}</button></nav>}
      <AuthForm key={mode} mode={mode} switchMode={switchMode}/>
    </section></main>
  </Shell>;
}
function AuthForm({ mode, switchMode }: { mode: Mode; switchMode(mode: Mode): void }) {
  const { t } = useLanguage();
  const formRef = useRef<HTMLFormElement>(null);
  const [consent, setConsent] = useState(false);
  const [message, setMessage] = useState(() => mode === 'login' && sessionStorage.getItem('login-notice') ? '登录状态已失效，请重新登录' : '');
  const [success, setSuccess] = useState(false), [phase, setPhase] = useState<'idle' | 'verifying' | 'submitting'>('idle');
  const [cooldown, setCooldown] = useState(0);
  const gate = useRef<CaptchaGate | null>(null), revision = useRef(0), submitting = useRef(false);
  const pendingRequest = useRef<AbortController | null>(null);
  const emailRef = useRef<HTMLInputElement>(null), consentRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    gate.current = new CaptchaGate(() => setPhase('idle'));  sessionStorage.removeItem('login-notice');
    const interrupt = () => {
      revision.current++;
      pendingRequest.current?.abort();
      submitting.current = false;
      gate.current?.cancel();
      setPhase('idle');
    };
    const restore = (event: PageTransitionEvent) => {
      if (!event.persisted) return;
      interrupt();
      setMessage('');
      if (mode !== 'reset' && localStorage.getItem('token')) location.replace('/devices');
    };
    window.addEventListener('pagehide', interrupt);
    window.addEventListener('pageshow', restore);
    return () => {
      revision.current++; pendingRequest.current?.abort(); gate.current?.dispose();
      window.removeEventListener('pagehide', interrupt); window.removeEventListener('pageshow', restore);
    };
  }, [mode]);
  useEffect(() => { if (!cooldown) return; const timer = setTimeout(() => setCooldown(value => value - 1), 1000); return () => clearTimeout(timer); }, [cooldown]);
  // Native inputs own their values: password managers and browser restoration
  // do not always dispatch React change events. Rendering status must never
  // overwrite the account visible to the user with an older state value.
  function field(name: keyof AuthValues) {
    const input = formRef.current?.elements.namedItem(name);
    return input instanceof HTMLInputElement ? input : undefined;
  }
  function readValues(): AuthValues {
    return { email: field('email')?.value || '', password: field('password')?.value || '',
      confirm: field('confirm')?.value || '', code: field('code')?.value || '' };
  }
  function changed() {
    revision.current++; pendingRequest.current?.abort(); submitting.current = false;
    gate.current?.cancel(); setPhase('idle'); setMessage(''); setSuccess(false);
  }
  function isCurrent(current: number, values: AuthValues) {
    if (current !== revision.current) return false;
    const actual = readValues();
    if (Object.keys(values).some(key => actual[key as keyof AuthValues] !== values[key as keyof AuthValues])) {
      changed(); setMessage('表单内容已变化，请重新提交'); return false;
    }
    return true;
  }
  function agreed() {
    if (mode === 'reset' || consent) return true;
    setMessage('请先阅读并同意用户协议和隐私政策'); consentRef.current?.focus(); return false;
  }
  async function perform(kind: 'login' | 'send' | 'submit', token: CaptchaToken | null, current: number, values: AuthValues) {
    if (submitting.current || !isCurrent(current, values)) return;
    const controller = new AbortController(); pendingRequest.current = controller;
    submitting.current = true; setPhase('submitting'); setMessage(''); setSuccess(false);
    try {
      if (kind === 'send') {
        await request(mode === 'reset' ? '/v1/user/password-reset/send-code' : '/v1/user/send-code', { method: 'POST', anonymous: true, signal: controller.signal, body: { email: values.email.trim(), ...captchaFields(token) } });
        if (!isCurrent(current, values)) return;
        setCooldown(60); setSuccess(true); setMessage(mode === 'reset' ? '如该邮箱已注册，验证码将发送至该邮箱' : '验证码已发送');
      } else {
        const path = kind === 'login' ? '/v1/user/login' : mode === 'reset' ? '/v1/user/password-reset' : '/v1/user/register';
        const body = kind === 'login' ? { email: values.email.trim(), password: values.password, ...captchaFields(token) } : { email: values.email.trim(), password: values.password, code: values.code };
        const result = await request<{ token: string }>(path, { method: 'POST', anonymous: true, signal: controller.signal, body });
        if (!isCurrent(current, values)) return;
        clearSession();
        if (mode === 'reset') { setSuccess(true); setMessage('密码已重置，请使用新密码登录'); for (const name of ['password', 'confirm'] as const) { const input = field(name); if (input) input.value = ''; } }
        else { localStorage.setItem('token', result.token); location.assign('/devices'); }
      }
    } catch (error) {
      if (current === revision.current) setMessage(error instanceof ApiError && error.code === 401 && kind === 'login' ? '邮箱或密码错误' : errorText(error));
    } finally {
      if (pendingRequest.current === controller) pendingRequest.current = null;
      if (current === revision.current) { submitting.current = false; setPhase('idle'); if (token) gate.current?.reset(); }
    }
  }
  async function verify(kind: 'login' | 'send') {
    if (submitting.current || !agreed()) return;
    if (!emailRef.current?.reportValidity()) return;
    const current = revision.current, values = readValues();
    setPhase('verifying'); setMessage('');
    try { await gate.current?.open(token => { void perform(kind, token, current, values); }); }
    catch { if (current === revision.current) { setPhase('idle'); setMessage('人机验证加载失败，请重试'); } }
  }
  function submit(event: FormEvent) {
    event.preventDefault();
    if (submitting.current || !agreed()) return;
    if (mode === 'login') { void verify('login'); return; }
    const values = readValues();
    if (values.password !== values.confirm) { setMessage('两次密码不一致'); return; }
    void perform('submit', null, revision.current, values);
  }
  return <form ref={formRef} onSubmit={submit}>
    <fieldset disabled={phase === 'submitting'}>
      <label>{t(mode === 'reset' ? '注册邮箱' : '邮箱')}<input ref={emailRef} type="email" name="email" autoComplete={mode === 'reset' ? 'email' : 'username'} autoCapitalize="none" spellCheck={false} required defaultValue="" onInput={() => { changed(); const input = field('code'); if (input) input.value = ''; }} placeholder={t('请输入邮箱地址')}/></label>
      {mode !== 'login' && <label>{t('邮箱验证码')}<div className="code-row"><input aria-label={t('邮箱验证码')} name="code" defaultValue="" onInput={event => { event.currentTarget.value = event.currentTarget.value.replace(/\D/g, '').slice(0, 6); changed(); }} inputMode="numeric" pattern="[0-9]{6}" autoComplete="one-time-code" required placeholder={t('6 位验证码')}/><button type="button" disabled={cooldown > 0} onClick={() => void verify('send')}>{cooldown ? t('{p0}s 后重新获取', { p0: cooldown }) : t('获取验证码')}</button></div></label>}
      <PasswordField label={mode === 'login' ? '密码' : mode === 'reset' ? '新密码' : '设置密码'} required minLength={mode === 'login' ? undefined : 6} name="password" defaultValue="" onInput={changed} autoComplete={mode === 'login' ? 'current-password' : 'new-password'} placeholder={t(mode === 'login' ? '请输入密码' : '至少 6 位')}/>
      {mode !== 'login' && <PasswordField label={mode === 'reset' ? '确认新密码' : '确认密码'} autoComplete="new-password" minLength={6} required name="confirm" defaultValue="" onInput={changed} placeholder={t('再次输入密码')}/>}
      {mode === 'reset' ? <p className="legal"><LegalLinks/></p> : <div className="legal consent"><input ref={consentRef} id="consent" type="checkbox" checked={consent} onChange={event => { changed(); setConsent(event.target.checked); }}/><div><label htmlFor="consent">{t('我已阅读并同意')}</label> <LegalLinks/></div></div>}
      <button className="primary wide" type="submit">{t(phase === 'submitting' ? '提交中…' : mode === 'login' ? '登录' : mode === 'register' ? '注册' : '重置密码')}</button>
      {mode === 'login' && <p className="right"><button className="link" type="button" onClick={() => switchMode('reset')}>{t('忘记密码？')}</button></p>}
    </fieldset>
    {phase === 'verifying' && <p role="status" className="sr-only">{t('请完成人机验证')}</p>}
    {message && <p role={success ? 'status' : 'alert'} className={success ? 'success' : 'error'}>{t(message)}</p>}
    {mode === 'reset' && success && message === '密码已重置，请使用新密码登录' && <button type="button" className="wide" onClick={() => switchMode('login')}>{t('返回登录')}</button>}
  </form>;
}
