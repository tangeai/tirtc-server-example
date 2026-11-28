import { afterBrowserLogin } from '../../platform/console-session';
import { tr, usePageLanguage } from '../../platform/translate';
import Taro, { useLoad } from '@tarojs/taro';
import { Button } from '../../platform/button';
import { Checkbox, Image, Input, Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Notice } from '../../components/ui';
import { ApiError, errorText, request, signIn, signOut, rememberedEmail, token } from '../../services/api';
import { Captcha, type CaptchaConfig, type CaptchaHandle } from '../../platform/captcha';
import eye from '../../assets/auth/eye.png';
import eyeOff from '../../assets/auth/eye-off.png';
import brandRobot from '../../generated/auth-logo.json';

type Mode = 'login' | 'register' | 'reset';
export default function Login() {
  usePageLanguage();
  const notification = useRef('');
  const [mode, setMode] = useState<Mode>('login');
  const [email, setEmail] = useState(''), [password, setPassword] = useState(''), [confirm, setConfirm] = useState(''), [code, setCode] = useState('');
  const [consent, setConsent] = useState(false), [visible, setVisible] = useState(false), [busy, setBusy] = useState(false), [cooldown, setCooldown] = useState(0);
  const [error, setError] = useState(''), [notice, setNotice] = useState(''), [config, setConfig] = useState<CaptchaConfig>();
  const [configLoading, setConfigLoading] = useState(true), [configError, setConfigError] = useState('');
  const captcha = useRef<CaptchaHandle>(null), alive = useRef(true), lock = useRef(false), configPending = useRef(false);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useEffect(() => { if (!cooldown) return; const timer = setTimeout(() => setCooldown(n => Math.max(n - 1, 0)), 1000); return () => clearTimeout(timer); }, [cooldown]);
  async function loadConfig() {
    if (configPending.current) return;
    configPending.current = true; setConfigLoading(true); setConfigError('');
    try {
      const result = await request<CaptchaConfig>('/v1/config/captcha');
      if (!result || typeof result.enabled !== 'boolean') throw new ApiError(-1, '验证配置返回异常，请重新加载');
      if (alive.current) setConfig(result);
    }
    catch (e) { if (alive.current) setConfigError(errorText(e)); }
    finally { configPending.current = false; if (alive.current) setConfigLoading(false); }
  }
  useLoad(options => {
    if (options.notification && /^[0-9a-f-]{36}$/.test(options.notification)) notification.current = options.notification;
    const browserPath = process.env.TARO_ENV === 'h5' ? location.pathname : '';
    if (options.mode === 'reset' || browserPath === '/forgot-password') setMode('reset');
    else if (token()) { if (process.env.TARO_ENV === 'h5') location.replace('/devices'); else void Taro.switchTab({ url: '/pages/devices/index' }); return; }
    else if (browserPath === '/register' || options.tab === 'register') setMode('register');
    setEmail(rememberedEmail());
    if (options.expired) setNotice(tr("登录状态已过期，请重新登录"));
    void loadConfig();
  });
  function switchMode(next: Mode) { if (lock.current || mode === next) return;
    if (process.env.TARO_ENV === 'h5') { location.assign(next === 'reset' ? '/forgot-password' : '/' + next); return; }
    setMode(next); setError(''); setNotice(''); setCode(''); setPassword(''); setConfirm(''); }
  async function submit(sendCode = false) {
    if (lock.current || (sendCode && cooldown)) return;
    const submitted = { email, password, confirm, code };
    if (process.env.TARO_ENV === 'h5') {
      // Password managers can update native input values without firing an input event.
      for (const key of ['email', 'password', 'confirm', 'code'] as const) {
        const input = document.querySelector<HTMLInputElement>('.auth-page input[name="' + key + '"]');
        if (input) submitted[key] = input.value;
      }
    }
    setError(''); setNotice('');
    if (mode !== 'reset' && !consent) { setError(tr("请先阅读并同意用户协议和隐私政策")); return; }
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(submitted.email.trim())) { setError(tr("请输入有效的邮箱地址")); return; }
    if (!sendCode && !submitted.password) { setError(tr("请输入密码")); return; }
    if (!sendCode && mode !== 'login' && (!/^\d{6}$/.test(submitted.code) || submitted.password.length < 6 || submitted.password !== submitted.confirm)) {
      setError(!/^\d{6}$/.test(submitted.code) ? '请输入 6 位邮箱验证码' : submitted.password.length < 6 ? '密码至少 6 位' : '两次密码不一致'); return;
    }
    lock.current = true; setBusy(true);
    try {
      if ((sendCode || mode === 'login') && (!config || !captcha.current)) throw new ApiError(-1, '验证配置尚未就绪，请重新加载');
      const proof = sendCode || mode === 'login' ? await captcha.current!.verify() : undefined;
      if (!alive.current) return;
      const payload = { email: submitted.email.trim(), ...(proof ? { captcha: proof } : {}) };
      if (sendCode) {
        await request(mode === 'reset' ? '/v1/user/password-reset/send-code' : '/v1/user/send-code', 'POST', payload);
        if (alive.current) { setCooldown(60); setNotice(mode === 'reset' ? '如该邮箱已注册，验证码将发送至该邮箱' : '验证码已发送，请查看邮箱'); }
      } else {
        const path = mode === 'login' ? '/v1/user/login' : mode === 'reset' ? '/v1/user/password-reset' : '/v1/user/register';
        const result = await request<{ token: string }>(path, 'POST', { ...payload, password: submitted.password, ...(mode !== 'login' ? { code: submitted.code } : {}) });
        if (!alive.current) return;
        if (mode === 'reset') { signOut(); setMode('login'); setPassword(''); setConfirm(''); setCode(''); setNotice(tr("密码已重置，请使用新密码登录")); }
        else { if (!result?.token) throw new ApiError(-1, '登录响应异常，请重试'); signIn(result.token, submitted.email); if (process.env.TARO_ENV === 'h5') { location.replace(notification.current ? '/notifications?ref=' + encodeURIComponent(notification.current) : afterBrowserLogin()); return; } await Taro.reLaunch({ url: notification.current ? '/pages/notifications/index?ref=' + encodeURIComponent(notification.current) : '/pages/devices/index' }); }
      }
    } catch (e) { if (alive.current) setError(errorText(e)); }
    finally { lock.current = false; if (alive.current) setBusy(false); }
  }
  useEffect(() => {
    if (process.env.TARO_ENV !== 'h5') return;
    const onEnter = (event: KeyboardEvent) => {
      if (event.key !== 'Enter' || event.repeat || event.isComposing || !config || busy) return;
      if (!(event.target instanceof HTMLInputElement) || !event.target.closest('.auth-fields')) return;
      event.preventDefault();
      void submit();
    };
    document.addEventListener('keydown', onEnter, true);
    return () => document.removeEventListener('keydown', onEnter, true);
  });
  return <View className={'page auth-page auth-' + mode}>
    {process.env.TARO_ENV !== 'h5' && <View className='auth-brand'><Image className='auth-brand-robot' src={brandRobot} mode='aspectFit' ariaLabel={tr("小钛机器人")} /><View><Text className='auth-brand-name'>{tr("小钛")}</Text><Text className='auth-tagline'>{tr("连接设备，随时相伴")}</Text></View></View>}
    <View className='auth-panel'>
      {process.env.TARO_ENV === 'h5' && mode !== 'reset' && <Text className='web-auth-title'>{tr(mode === 'login' ? '登录账号' : '注册账号')}</Text>}
      {mode === 'reset' ? <View className='auth-heading'><Text>{tr("找回密码")}</Text><Text className='auth-heading-note'>{tr("通过邮箱验证身份")}</Text></View> : <View className='auth-tabs'>{(['login', 'register'] as const).map(value => <Button id={'auth-' + value} key={value} className={'auth-tab ' + (mode === value ? 'is-active' : '')} onClick={() => switchMode(value)}><Text>{value === 'login' ? tr("登录") : tr("注册")}</Text><View className='auth-tab-line' /></Button>)}</View>}
      <View className='auth-fields'>
        <View className='auth-field'><Text className='auth-label'>{tr("邮箱")}</Text><Input className='auth-input' name='email' nativeProps={{ autoComplete: 'username', inputMode: 'email', 'aria-label': tr('邮箱') }} value={email} disabled={busy} maxlength={254} placeholder={tr("请输入邮箱地址")} placeholderClass='auth-placeholder' onInput={e => setEmail(e.detail.value)} /></View>
        {mode !== 'login' && <View className='auth-field'><Text className='auth-label'>{tr("验证码")}</Text><Input className='auth-input' name='code' nativeProps={{ autoComplete: 'one-time-code', inputMode: 'numeric', 'aria-label': tr('验证码') }} value={code} type={process.env.TARO_ENV === 'h5' ? 'text' : 'number'} maxlength={6} disabled={busy} placeholder={tr("6 位验证码")} placeholderClass='auth-placeholder' onInput={e => setCode(e.detail.value.normalize('NFKC').replace(/\D/g, '').slice(0, 6))} /><Button className='auth-inline-button' disabled={busy || cooldown > 0 || !config} onClick={() => void submit(true)}>{cooldown ? `${cooldown}s 后重发` : tr("获取验证码")}</Button></View>}
        <View className='auth-field'><Text className='auth-label'>{tr("密码")}</Text><Input className='auth-input' name='password' nativeProps={{ autoComplete: mode === 'login' ? 'current-password' : 'new-password', 'aria-label': tr('密码') }} value={password} password={!visible} maxlength={-1} disabled={busy} placeholder={mode === 'login' ? tr("请输入密码") : tr("至少 6 位密码")} placeholderClass='auth-placeholder' onInput={e => setPassword(e.detail.value)} /><Button className='auth-inline-button auth-visibility' ariaLabel={visible ? tr("隐藏密码") : tr("显示密码")} disabled={busy} onClick={() => setVisible(value => !value)}><Image className='auth-eye' src={visible ? eye : eyeOff} mode='aspectFit' /></Button></View>
        {mode !== 'login' && <View className='auth-field'><Text className='auth-label'>{tr("确认密码")}</Text><Input className='auth-input' name='confirm' nativeProps={{ autoComplete: 'new-password', 'aria-label': tr('确认密码') }} value={confirm} password={!visible} maxlength={-1} disabled={busy} placeholder={tr("再次输入密码")} placeholderClass='auth-placeholder' onInput={e => setConfirm(e.detail.value)} /></View>}
      </View>
      {config && <Captcha ref={captcha} config={config} />}
      <View className='auth-feedback'><Notice error>{configError || error}</Notice><Notice>{notice}</Notice>{!config && !configLoading && configError && <Button className='auth-retry' disabled={busy} onClick={() => void loadConfig()}>{tr("重新加载验证配置")}</Button>}</View>
      {mode !== 'reset' && <View className='auth-consent'><Checkbox className='auth-checkbox' value='agree' checked={consent} disabled={busy} color='#07c160' onClick={() => { if (!busy) setConsent(!consent); }} /><Text className='auth-consent-copy'><Text onClick={() => { if (!busy) setConsent(value => !value); }}>{tr("我已阅读并同意")}</Text><Text className='auth-link' onClick={() => void Taro.navigateTo({ url: '/pages/legal/index?kind=terms' })}>{tr("《用户协议》")}</Text>{tr("和")}<Text className='auth-link' onClick={() => void Taro.navigateTo({ url: '/pages/legal/index?kind=privacy' })}>{tr("《隐私政策》")}</Text></Text></View>}
      <Button className='auth-submit' loading={busy || configLoading} disabled={busy || !config || configLoading} onClick={() => void submit()}>{configLoading ? tr("正在加载验证…") : busy ? tr("处理中") : !config ? tr("验证配置未就绪") : mode === 'login' ? tr("登录") : mode === 'reset' ? tr("重置密码") : tr("注册并登录")}</Button>
      <View className='auth-footer'>{mode === 'register' && <Text className='auth-footer-copy'>{tr("已有账号？")}</Text>}<Button key={mode} className='auth-link auth-footer-button' disabled={busy} onClick={() => {
        if (mode === 'reset' && token()) {
          if (process.env.TARO_ENV === 'h5') location.replace('/devices');
          else void Taro.switchTab({ url: '/pages/devices/index' });
        } else switchMode(mode === 'login' ? 'reset' : 'login');
      }}>{mode === 'login' ? tr("忘记密码？") : mode === 'register' ? tr("去登录") : token() ? tr("返回设备列表") : tr("返回登录")}</Button></View>
    </View>
  </View>;
}
