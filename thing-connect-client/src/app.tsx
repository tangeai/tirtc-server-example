import { guardBrowserPage } from './platform/console-session';
import { ConsoleHeader } from './platform/console-header';
import type { PropsWithChildren } from 'react';
import { useLaunch, useDidShow } from '@tarojs/taro';
import { initializeMedia, refreshIncomingMedia } from './platform/media';
import './app.css';

guardBrowserPage();

export default function App({ children }: PropsWithChildren) {
  useLaunch(initializeMedia);
  useDidShow(refreshIncomingMedia);
  return process.env.TARO_ENV === 'h5' ? <><ConsoleHeader />{children}</> : children;
}
