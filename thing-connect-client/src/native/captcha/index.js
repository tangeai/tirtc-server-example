Component({
  properties: { captchaId: String },
  methods: {
    show() {
      const widget = this.selectComponent('#verification');
      const show = widget && (widget.popUp || widget.popup || widget.show);
      if (!show) throw new Error('验证控件尚未就绪');
      // A successful challenge is single-use even when the subsequent login fails.
      // Do not reset before the first popup creates the plugin's inner instance.
      if (this._verified) {
        widget.reset();
        this._verified = false;
      }
      show.call(widget);
    },
    reset() { this.selectComponent('#verification')?.reset?.(); },
    onVerify(event) {
      const [error, value] = event.detail || [];
      if (!error && value) this._verified = true;
      this.triggerEvent('verify', event.detail);
    },
    onClose() { this.triggerEvent('close'); },
  },
});
