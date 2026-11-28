import { tr, usePageLanguage } from '../../platform/translate';
import Taro, { useDidShow } from '@tarojs/taro';
import { Text, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Action, Notice, RobotIcon } from '../../components/ui';
import { request, signOut, token, errorText } from '../../services/api';
export default function Account() {
  usePageLanguage();
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
    const result = await Taro.showModal({ title: tr("退出登录"), content: tr("设备绑定和已保存的设置会保留。"), confirmText: tr("退出登录") });
    if (!result.confirm || !alive.current) return;
    signOut();
    if (process.env.TARO_ENV === 'h5') location.replace('/login');
    else await Taro.reLaunch({ url: '/pages/login/index' });
  }
  return <View className='page account-page'>
    <View className='card account-profile'><View className='account-avatar' ariaLabel={tr("小钛机器人")}><RobotIcon /></View><View className='grow'><Text className='title'>{tr("我的账号")}</Text><Text className='account-email'>{loading ? tr("正在加载…") : email || tr("账号信息暂不可用")}</Text></View></View>
    <Notice error>{error}</Notice>{error && <Action disabled={loading} onClick={() => void load()}>{tr("重试")}</Action>}
    <View className='card account-links'><Action onClick={() => void Taro.navigateTo({ url: '/pages/login/index?mode=reset' })}><Text>{tr("修改密码")}</Text><View className="settings-chevron" ariaHidden /></Action><Action onClick={() => void Taro.navigateTo({ url: '/pages/legal/index?kind=terms' })}><Text>{tr("用户协议")}</Text><View className="settings-chevron" ariaHidden /></Action><Action onClick={() => void Taro.navigateTo({ url: '/pages/legal/index?kind=privacy' })}><Text>{tr("隐私政策")}</Text><View className="settings-chevron" ariaHidden /></Action></View>
    <View className='wide'><Action danger onClick={() => void logout()}>{tr("退出登录")}</Action></View>
  </View>;
}
