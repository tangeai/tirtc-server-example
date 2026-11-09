export default definePageConfig({
  navigationBarTitleText: '登录 / 注册',
  ...(process.env.TARO_ENV === 'weapp' ? { usingComponents: { 'auth-captcha': '../../native/captcha/index' } } : {}),
});
