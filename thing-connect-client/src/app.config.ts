export default defineAppConfig({
  pages: ['pages/login/index', 'pages/devices/index', 'pages/account/index', 'pages/contacts/index', 'pages/bind/index', 'pages/legal/index', 'pages/playback/index', 'pages/recognition/index', 'pages/room/index', 'pages/history/index', 'pages/roles/index'],
  tabBar: { color: '#939b96', selectedColor: '#07b75b', backgroundColor: '#ffffff', borderStyle: 'white', list: [
    { pagePath: 'pages/devices/index', text: '设备', iconPath: 'assets/tab/devices.png', selectedIconPath: 'assets/tab/devices-active.png' },
    { pagePath: 'pages/account/index', text: '我的', iconPath: 'assets/tab/account.png', selectedIconPath: 'assets/tab/account-active.png' },
  ] },
  window: { navigationBarTitleText: '小钛', navigationBarBackgroundColor: '#ffffff', navigationBarTextStyle: 'black', backgroundColor: '#f7f7f7' },
  ...(process.env.TARO_ENV === 'weapp' ? { plugins: { captcha: { version: '1.4.1', provider: 'wxb7c8f9ea9ceb4663' }, 'wmpf-voip': { version: 'latest', provider: 'wxf830863afde621eb' } } } : {}),
});
