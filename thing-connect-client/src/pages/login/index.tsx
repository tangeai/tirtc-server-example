import Taro, { useLoad } from '@tarojs/taro';
import { Button, Checkbox, Image, Input, Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Notice } from '../../components/ui';
import { ApiError, errorText, request, signIn, signOut, rememberedEmail, token } from '../../services/api';
import { Captcha, type CaptchaConfig, type CaptchaHandle } from '../../platform/captcha';
import eye from '../../assets/auth/eye.png';
import eyeOff from '../../assets/auth/eye-off.png';
import brandRobot from '../../generated/auth-logo.json';

type Mode = 'login' | 'register' | 'reset';
export default function Login() {
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
    if (options.mode === 'reset') setMode('reset');
    else if (token()) { void Taro.switchTab({ url: '/pages/devices/index' }); return; }
    setEmail(rememberedEmail());
    if (options.expired) setNotice('登录状态已过期，请重新登录');
    void loadConfig();
  });
  function switchMode(next: Mode) { if (lock.current) return; setMode(next); setError(''); setNotice(''); setCode(''); setPassword(''); setConfirm(''); }
  async function submit(sendCode = false) {
    if (lock.current || (sendCode && cooldown)) return;
    setError(''); setNotice('');
    if (mode !== 'reset' && !consent) { setError('请先阅读并同意用户协议和隐私政策'); return; }
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) { setError('请输入有效的邮箱地址'); return; }
    if (!sendCode && !password) { setError('请输入密码'); return; }
    if (!sendCode && mode !== 'login' && (!/^\d{6}$/.test(code) || password.length < 6 || password !== confirm)) {
      setError(!/^\d{6}$/.test(code) ? '请输入 6 位邮箱验证码' : password.length < 6 ? '密码至少 6 位' : '两次密码不一致'); return;
    }
    lock.current = true; setBusy(true);
    try {
      if ((sendCode || mode === 'login') && (!config || !captcha.current)) throw new ApiError(-1, '验证配置尚未就绪，请重新加载');
      const proof = sendCode || mode === 'login' ? await captcha.current!.verify() : undefined;
      if (!alive.current) return;
      const payload = { email: email.trim(), ...(proof ? { captcha: proof } : {}) };
      if (sendCode) {
        await request(mode === 'reset' ? '/v1/user/password-reset/send-code' : '/v1/user/send-code', 'POST', payload);
        if (alive.current) { setCooldown(60); setNotice(mode === 'reset' ? '如该邮箱已注册，验证码将发送至该邮箱' : '验证码已发送，请查看邮箱'); }
      } else {
        const path = mode === 'login' ? '/v1/user/login' : mode === 'reset' ? '/v1/user/password-reset' : '/v1/user/register';
        const result = await request<{ token: string }>(path, 'POST', { ...payload, password, ...(mode !== 'login' ? { code } : {}) });
        if (!alive.current) return;
        if (mode === 'reset') { signOut(); setMode('login'); setPassword(''); setConfirm(''); setCode(''); setNotice('密码已重置，请使用新密码登录'); }
        else { if (!result?.token) throw new ApiError(-1, '登录响应异常，请重试'); signIn(result.token, email); await Taro.reLaunch({ url: '/pages/devices/index' }); }
      }
    } catch (e) { if (alive.current) setError(errorText(e)); }
    finally { lock.current = false; if (alive.current) setBusy(false); }
  }
  return <View className={'page auth-page auth-' + mode}>
    <View className='auth-brand'><Image className='auth-brand-robot' src={brandRobot} mode='aspectFit' ariaLabel='小钛机器人' /><View><Text className='auth-brand-name'>小钛</Text><Text className='auth-tagline'>连接设备，随时相伴</Text></View></View>
    <View className='auth-panel'>
      {mode === 'reset' ? <View className='auth-heading'><Text>找回密码</Text><Text className='auth-heading-note'>通过邮箱验证身份</Text></View> : <View className='auth-tabs'>{(['login', 'register'] as const).map(value => <View id={'auth-' + value} key={value} className={'auth-tab ' + (mode === value ? 'is-active' : '')} onClick={() => switchMode(value)}><Text>{value === 'login' ? '登录' : '注册'}</Text><View className='auth-tab-line' /></View>)}</View>}
      <View className='auth-fields'>
        <View className='auth-field'><Text className='auth-label'>邮箱</Text><Input className='auth-input' value={email} disabled={busy} maxlength={254} placeholder='请输入邮箱地址' placeholderClass='auth-placeholder' onInput={e => setEmail(e.detail.value)} /></View>
        {mode !== 'login' && <View className='auth-field'><Text className='auth-label'>验证码</Text><Input className='auth-input' value={code} type='number' maxlength={6} disabled={busy} placeholder='6 位验证码' placeholderClass='auth-placeholder' onInput={e => setCode(e.detail.value.normalize('NFKC').replace(/\D/g, '').slice(0, 6))} /><Button className='auth-inline-button' disabled={busy || cooldown > 0 || !config} onClick={() => void submit(true)}>{cooldown ? `${cooldown}s 后重发` : '获取验证码'}</Button></View>}
        <View className='auth-field'><Text className='auth-label'>密码</Text><Input className='auth-input' value={password} password={!visible} maxlength={-1} disabled={busy} placeholder={mode === 'login' ? '请输入密码' : '至少 6 位密码'} placeholderClass='auth-placeholder' onInput={e => setPassword(e.detail.value)} /><Button className='auth-inline-button auth-visibility' ariaLabel={visible ? '隐藏密码' : '显示密码'} disabled={busy} onClick={() => setVisible(value => !value)}><Image className='auth-eye' src={visible ? eye : eyeOff} mode='aspectFit' /></Button></View>
        {mode !== 'login' && <View className='auth-field'><Text className='auth-label'>确认密码</Text><Input className='auth-input' value={confirm} password={!visible} maxlength={-1} disabled={busy} placeholder='再次输入密码' placeholderClass='auth-placeholder' onInput={e => setConfirm(e.detail.value)} /></View>}
      </View>
      {config && <Captcha ref={captcha} config={config} />}
      <View className='auth-feedback'><Notice error>{configError || error}</Notice><Notice>{notice}</Notice>{!config && !configLoading && configError && <Button className='auth-retry' disabled={busy} onClick={() => void loadConfig()}>重新加载验证配置</Button>}</View>
      {mode !== 'reset' && <View className='auth-consent'><Checkbox className='auth-checkbox' value='agree' checked={consent} disabled={busy} color='#07c160' onClick={() => { if (!busy) setConsent(!consent); }} /><Text className='auth-consent-copy'>我已阅读并同意<Text className='auth-link' onClick={() => void Taro.navigateTo({ url: '/pages/legal/index?kind=terms' })}>《用户协议》</Text>和<Text className='auth-link' onClick={() => void Taro.navigateTo({ url: '/pages/legal/index?kind=privacy' })}>《隐私政策》</Text></Text></View>}
      <Button className='auth-submit' loading={busy || configLoading} disabled={busy || !config || configLoading} onClick={() => void submit()}>{configLoading ? '正在加载验证…' : busy ? '处理中' : !config ? '验证配置未就绪' : mode === 'login' ? '登录' : mode === 'reset' ? '重置密码' : '注册并登录'}</Button>
      <View className='auth-footer'>{mode === 'register' && <Text className='auth-footer-copy'>已有账号？</Text>}<Button key={mode} className='auth-link auth-footer-button' disabled={busy} onClick={() => switchMode(mode === 'login' ? 'reset' : 'login')}>{mode === 'login' ? '忘记密码？' : mode === 'register' ? '去登录' : '返回登录'}</Button></View>
    </View>
  </View>;
}
