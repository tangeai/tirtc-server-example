import type { PropsWithChildren } from 'react';
import { useLaunch, useDidShow } from '@tarojs/taro';
import { initializeMedia, refreshIncomingMedia } from './platform/media';
import './app.css';

export default function App({ children }: PropsWithChildren) {
  useLaunch(initializeMedia);
  useDidShow(refreshIncomingMedia);
  return children;
}
