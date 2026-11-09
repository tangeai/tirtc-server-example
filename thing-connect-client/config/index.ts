import { defineConfig } from '@tarojs/cli';

export default defineConfig({
  projectName: 'thingconnect-client',
  date: '2026-11-09',
  designWidth: 750,
  deviceRatio: { 640: 2.34 / 2, 750: 1, 828: 1.81 / 2 },
  sourceRoot: 'src',
  outputRoot: `dist/${process.env.TARO_ENV || 'weapp'}`,
  framework: 'react',
  compiler: 'vite',
  plugins: ['@tarojs/plugin-platform-weapp', '@tarojs/plugin-platform-h5'],
  defineConstants: {
    API_ORIGIN: JSON.stringify(process.env.TARO_APP_API_ORIGIN ?? (process.env.TARO_ENV === 'h5' ? '' : 'https://xiaotai.chat')),
    WX_MODEL_ID: JSON.stringify(process.env.TARO_APP_WX_MODEL_ID || 'HRHY_vJ9mHI2KQhd6yvj9Q'),
  },
  // UTF-8 keeps Chinese strings compact without changing their runtime value.
  terser: { config: { output: { ascii_only: false } } },
  mini: {},
  h5: { publicPath: '/', router: { mode: 'hash' } },
});
