import Taro, { useLoad } from '@tarojs/taro';
import { WebView, View } from '@tarojs/components';
import { useEffect, useRef, useState } from 'react';
import { Notice } from '../../components/ui';
import { request, token, ApiError, errorText } from '../../services/api';
import type { Device } from '../../domain/devices';
export default function Playback() {
  const [src, setSrc] = useState(''), [error, setError] = useState('');
  const alive = useRef(true);
  useEffect(() => () => { alive.current = false; }, []);
  useLoad(options => {
    if (process.env.TARO_ENV === 'weapp') void Taro.hideShareMenu({ menus: ['shareAppMessage', 'shareTimeline'] });
    void (async () => {
      try {
        const id = options.device_id || '';
        const device = (await request<Device[]>('/v1/user/device/list')).find(d => d.device_id === id && d.status === 1);
        if (!device) throw new ApiError(40300, '设备不属于当前账号');
        if (!device.online) throw new ApiError(-1, '设备离线，请返回设备列表刷新');
        const origin = API_ORIGIN.replace(/\/$/, '');
        if (!/^https:\/\/[a-z0-9.-]+(?::\d+)?$/i.test(origin)) throw new ApiError(-1, '播放服务地址不可用');
        if (alive.current) setSrc(`${origin}/player?device_id=${encodeURIComponent(id)}&source=miniprogram#mini_token=${encodeURIComponent(token())}`);
      } catch (e) { if (alive.current) setError(errorText(e)); }
    })();
  });
  return error ? <View className='page'><Notice error>{error}</Notice></View> : src ? <WebView src={src} onError={() => { setSrc(''); setError('播放器加载失败，请检查网络后重试'); }} /> : <View className='page'>正在连接设备…</View>;
}
