import Taro, { useLoad } from '@tarojs/taro';
import { RichText, View } from '@tarojs/components';
import { useState } from 'react';
import terms from '../../generated/terms.json';
import privacy from '../../generated/privacy.json';
export default function Legal() {
  const [kind, setKind] = useState('');
  useLoad(options => { setKind(options.kind || ''); void Taro.setNavigationBarTitle({ title: options.kind === 'privacy' ? '隐私政策' : '用户协议' }); });
  return <View className='page legal'><View className='card'>{kind === 'privacy' || kind === 'terms' ? <RichText nodes={kind === 'privacy' ? privacy : terms} /> : '页面不存在，请返回重试'}</View></View>;
}
