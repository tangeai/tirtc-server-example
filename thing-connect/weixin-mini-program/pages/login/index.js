// thing-connect/weixin-mini-program/pages/login/index.js
const { userApi } = require('../../utils/api')
const { clearSession, showExpiredToast } = require('../../utils/session')

Page({
  data: {
    activeTab: 'login',
    captchaId: '',
    captchaProvider: '',
    captchaEnabled: false,
    captchaSupported: false,
    // login
    loginEmail: '',
    loginPassword: '',
    loginLoading: false,
    _loginValidate: '',
    // register
    regEmail: '',
    regCode: '',
    regPassword: '',
    regPassword2: '',
    regLoading: false,
    codeLoading: false,
    codeCooldown: 0,
    _regValidate: '',
    errMsg: '',
    showLoginPwd: false,
    showRegPwd: false,
    showRegPwd2: false,
  },

  onLoad(options = {}) {
    if (options.expired === '1') clearSession()
    const token = wx.getStorageSync('token')
    if (token) {
      wx.redirectTo({ url: '/pages/devices/index' })
      return
    }
    this._loadCaptchaId()
  },

  onShow() {
    showExpiredToast()
  },

  async _loadCaptchaId() {
    try {
      const res = await userApi('/v1/config/captcha', 'GET')
      if (res && res.data) {
        const cfg = res.data
        const captchaId = (cfg.public_config && (cfg.public_config.mini_program_captcha_id || cfg.public_config.captcha_id)) || cfg.captcha_id || ''
        const provider = cfg.provider || ''
        const supported = !cfg.enabled || provider === 'yidun'
        this.setData({ captchaId, captchaProvider: provider, captchaEnabled: !!cfg.enabled, captchaSupported: supported })
      }
    } catch (_) {}
  },

  switchTab(e) {
    this.setData({ activeTab: e.currentTarget.dataset.tab, errMsg: '' })
  },

  toggleLoginPwd() { this.setData({ showLoginPwd: !this.data.showLoginPwd }) },
  toggleRegPwd() { this.setData({ showRegPwd: !this.data.showRegPwd }) },
  toggleRegPwd2() { this.setData({ showRegPwd2: !this.data.showRegPwd2 }) },

  onLoginEmailInput(e) { this.setData({ loginEmail: e.detail.value }) },
  onLoginPasswordInput(e) { this.setData({ loginPassword: e.detail.value }) },
  onRegEmailInput(e) { this.setData({ regEmail: e.detail.value }) },
  onRegCodeInput(e) { this.setData({ regCode: e.detail.value }) },
  onRegPasswordInput(e) { this.setData({ regPassword: e.detail.value }) },
  onRegPassword2Input(e) { this.setData({ regPassword2: e.detail.value }) },

  // 易盾验证回调 — 登录
  onLoginCaptchaVerify(ev) {
    const [err, validate] = ev.detail
    if (err) return
    this.setData({ _loginValidate: validate })
    this._submitLogin(this._captchaPayload(validate))
  },

  // 易盾验证回调 — 注册发送验证码
  onRegCaptchaVerify(ev) {
    const [err, validate] = ev.detail
    if (err) return
    this.setData({ _regValidate: validate })
    this._submitSendCode(this._captchaPayload(validate))
  },

  _captchaPayload(token, metadata) {
    const { captchaId, captchaProvider } = this.data
    return { provider: captchaProvider, token: token || '', metadata: Object.assign({ captcha_id: captchaId || '' }, metadata || {}) }
  },

  doLogin() {
    const { loginEmail, loginPassword, captchaId, captchaEnabled, captchaSupported } = this.data
    if (!loginEmail) { this.setData({ errMsg: '请输入邮箱' }); return }
    if (!loginPassword) { this.setData({ errMsg: '请输入密码' }); return }
    this.setData({ errMsg: '' })
    if (captchaEnabled && !captchaSupported) { this.setData({ errMsg: '当前人机验证服务需要更新小程序客户端' }); return }
    if (captchaEnabled) {
      const captcha = captchaId && this.selectComponent('#captcha-login')
      const show = captcha && (captcha.popUp || captcha.popup || captcha.show)
      if (!show) { this.setData({ errMsg: '验证码加载失败，请重新打开页面' }); return }
      show.call(captcha)
    } else {
      this._submitLogin(null)
    }
  },

  async _submitLogin(captchaPayload) {
    const { loginEmail, loginPassword, captchaId, captchaProvider } = this.data
    this.setData({ loginLoading: true })
    try {
      const res = await userApi('/v1/user/login', 'POST', {
        email: loginEmail,
        password: loginPassword,
        captcha_id: captchaId || '',
        validate: captchaPayload ? captchaPayload.token : '',
        user: '',
        captcha: captchaProvider ? captchaPayload : null,
      })
      if (res.code === 200) {
        wx.setStorageSync('token', res.data.token)
        wx.redirectTo({ url: '/pages/devices/index' })
      } else {
        this.setData({ errMsg: res.msg || '登录失败' })
      }
    } catch (e) {
      this.setData({ errMsg: e.msg || '登录失败' })
    } finally {
      this.setData({ loginLoading: false, _loginValidate: '' })
      // 登录失败后重置验证码，下次弹出可重新操作
      const captcha = this.selectComponent('#captcha-login')
      if (captcha && captcha.reset) { captcha.reset() }
    }
  },

  doSendCode() {
    const { regEmail, captchaId, captchaEnabled, captchaSupported } = this.data
    if (!regEmail) { this.setData({ errMsg: '请输入邮箱' }); return }
    this.setData({ errMsg: '' })
    if (captchaEnabled && !captchaSupported) { this.setData({ errMsg: '当前人机验证服务需要更新小程序客户端' }); return }
    if (captchaEnabled) {
      const captcha = captchaId && this.selectComponent('#captcha-reg')
      const show = captcha && (captcha.popUp || captcha.popup || captcha.show)
      if (!show) { this.setData({ errMsg: '验证码加载失败，请重新打开页面' }); return }
      show.call(captcha)
    } else {
      this._submitSendCode(null)
    }
  },

  async _submitSendCode(captchaPayload) {
    const { regEmail, captchaId, captchaProvider } = this.data
    this.setData({ codeLoading: true })
    try {
      const res = await userApi('/v1/user/send-code', 'POST', {
        email: regEmail,
        captcha_id: captchaId || '',
        validate: captchaPayload ? captchaPayload.token : '',
        user: '',
        captcha: captchaProvider ? captchaPayload : null,
      })
      if (res.code === 200) {
        this._startCooldown()
      } else {
        this.setData({ errMsg: res.msg || '发送失败' })
      }
    } catch (e) {
      this.setData({ errMsg: e.msg || '发送失败' })
    } finally {
      this.setData({ codeLoading: false, _regValidate: '' })
      const captcha = this.selectComponent('#captcha-reg')
      if (captcha && captcha.reset) { captcha.reset() }
    }
  },

  _startCooldown() {
    this.setData({ codeCooldown: 60 })
    const timer = setInterval(() => {
      const n = this.data.codeCooldown - 1
      if (n <= 0) { clearInterval(timer); this.setData({ codeCooldown: 0 }) }
      else this.setData({ codeCooldown: n })
    }, 1000)
  },

  async doRegister() {
    const { regEmail, regCode, regPassword, regPassword2 } = this.data
    if (!regEmail) { this.setData({ errMsg: '请输入邮箱' }); return }
    if (!regCode) { this.setData({ errMsg: '请输入验证码' }); return }
    if (!regPassword) { this.setData({ errMsg: '请输入密码' }); return }
    if (regPassword !== regPassword2) { this.setData({ errMsg: '两次密码不一致' }); return }
    this.setData({ regLoading: true, errMsg: '' })
    try {
      const res = await userApi('/v1/user/register', 'POST', {
        email: regEmail,
        password: regPassword,
        code: regCode,
      })
      if (res.code === 200) {
        wx.setStorageSync('token', res.data.token)
        wx.redirectTo({ url: '/pages/devices/index' })
      } else {
        this.setData({ errMsg: res.msg || '注册失败' })
      }
    } catch (e) {
      this.setData({ errMsg: e.msg || '注册失败' })
    } finally {
      this.setData({ regLoading: false })
    }
  },
})
