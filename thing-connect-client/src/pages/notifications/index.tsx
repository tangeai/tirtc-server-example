import Taro, { useLoad } from '@tarojs/taro';
import { Text, View } from '@tarojs/components';
import { useRef, useState } from 'react';
import { Action, Notice } from '../../components/ui';
import { ApiError, errorText, request, token } from '../../services/api';

type Alert = { device_id: string; label_text?: string };

export default function Notifications() {
  const reference = useRef('');
  const [alert, setAlert] = useState<Alert>();
  const [error, setError] = useState('');

  async function load(ref: string) {
    setError('');
    try {
      const data = await request<Alert>('/v1/push/user/references/' + encodeURIComponent(ref));
      setAlert(data);
    } catch (e) {
      if (e instanceof ApiError && e.code === 41000) {
        await Taro.switchTab({ url: '/pages/devices/index' });
        await Taro.showToast({ title: '该提醒已失效', icon: 'none' });
      } else setError(errorText(e));
    }
  }

  useLoad(options => {
    const ref = options.ref || '';
    if (!token()) {
      void Taro.reLaunch({ url: '/pages/login/index' + (ref ? '?notification=' + encodeURIComponent(ref) : '') });
    } else if (ref) {
      reference.current = ref;
      void load(ref);
    } else if (options.device_id) {
      void Taro.redirectTo({ url: '/pages/recognition/index?device_id=' + encodeURIComponent(options.device_id) });
    } else void Taro.switchTab({ url: '/pages/devices/index' });
  });

  return <View className='page content-page'>
    {alert ? <View className='card'>
      <Text className='label'>识别标签</Text><Text className='title'>{alert.label_text || '未提供标签'}</Text>
      <Text className='muted'>设备 ID · {alert.device_id}</Text>
    </View> : error ? <><Notice error>{error}</Notice><Action onClick={() => void load(reference.current)}>重新加载</Action></> : <Notice>正在加载提醒详情…</Notice>}
  </View>;
}
