import Taro, { useDidShow } from '@tarojs/taro';
import { Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Action, Notice, RobotIcon } from '../../components/ui';
import { request, signOut, token, errorText } from '../../services/api';
export default function Account() {
  const [email, setEmail] = useState(''), [error, setError] = useState(''), [loading, setLoading] = useState(false);
  const alive = useRef(true), pending = useRef(false), loaded = useRef(false);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useDidShow(() => {
    if (!token()) { void Taro.reLaunch({ url: '/pages/login/index' }); return; }
    if (!loaded.current) void load();
  });
  async function load() {
    if (pending.current) return;
    pending.current = true; setLoading(true); setError('');
    try { const result = await request<{ email: string }>('/v1/user/me'); if (alive.current) { setEmail(result.email); loaded.current = true; } }
    catch (e) { if (alive.current) setError(errorText(e)); }
    finally { pending.current = false; if (alive.current) setLoading(false); }
  }
  async function logout() {
    const result = await Taro.showModal({ title: '退出登录', content: '设备绑定和已保存的设置会保留。', confirmText: '退出登录' });
    if (result.confirm && alive.current) { signOut(); await Taro.reLaunch({ url: '/pages/login/index' }); }
  }
  return <View className='page account-page'>
    <View className='card account-profile'><View className='account-avatar' ariaLabel='小钛机器人'><RobotIcon /></View><View className='grow'><Text className='title'>我的账号</Text><Text className='account-email'>{loading ? '正在加载…' : email || '账号信息暂不可用'}</Text></View></View>
    <Notice error>{error}</Notice>{error && <Action disabled={loading} onClick={() => void load()}>重试</Action>}
    <View className='card account-links'><Action onClick={() => void Taro.navigateTo({ url: '/pages/login/index?mode=reset' })}>修改密码　›</Action><Action onClick={() => void Taro.navigateTo({ url: '/pages/legal/index?kind=terms' })}>用户协议　›</Action><Action onClick={() => void Taro.navigateTo({ url: '/pages/legal/index?kind=privacy' })}>隐私政策　›</Action></View>
    <View className='wide'><Action danger onClick={() => void logout()}>退出登录</Action></View>
  </View>;
}
