export interface StreamProfile { [key: string]: unknown }
export interface Credential { token: string; app_id: string; profiles?: { stream?: StreamProfile } }
export interface Connection {
  connect(options: { deviceId: string; token: string }): Promise<void>;
  disconnect(): unknown;
  subscribeAudio(options: { streamId: number }): unknown;
  unsubscribeAudio(options: { streamId: number }): unknown;
  subscribeVideo(options: { streamId: number }): unknown;
  unsubscribeVideo(options: { streamId: number }): unknown;
}
interface Output { attach(): unknown; detach(): unknown }
interface Input extends Output { setOptions(options: { sampleRate: number }): void; start(): Promise<void>; stop(): unknown }
export interface MediaSDK {
  TiRtc: { initialize(options: unknown): void; videoOutputReady(): Promise<void> };
  TiRtcInitOptions(options: { appId: string }): unknown;
  TiRtcConn: new () => Connection;
  TiRtcAudioOutput(options: { connection: Connection; streamId: number }): Output;
  TiRtcVideoOutput(options: { connection: Connection; streamId: number }): Output;
  TiRtcAudioInput: new (options: { connection: Connection; streamId: number }) => Input;
}
export function streamIds(profile?: StreamProfile) {
  const read = (key: string, fallback: number) => { const value = profile?.[key]; return typeof value === 'number' && Number.isInteger(value) && value >= 0 && value <= 15 ? value : fallback; };
  return { upAudio: read('up_audio_streamid', 10), upVideo: read('up_video_streamid', 11), downAudio: read('down_audio_streamid', 14) };
}
export interface PlayerState { connected: boolean; connecting: boolean; talking: boolean; muted: boolean; message: string; profile?: StreamProfile }
interface Resources { connection: Connection; audio: Output; video: Output; input: Input; ids: ReturnType<typeof streamIds>; audioSubscribed: boolean; videoSubscribed: boolean }
const safely = (action: () => unknown) => { try { Promise.resolve(action()).catch(() => {}); } catch { /* Release the remaining resources even if one SDK operation fails. */ } };

// Owns the complete SDK session and its asynchronous lifetime. React owns UI
// state; the SDK owns only media resources and the canvas renderer.
export class MediaSession {
  private generation = 0;
  private resources?: Resources;
  private controller?: AbortController;
  private timeout?: ReturnType<typeof setTimeout>;
  private talkPending = false;
  private state: PlayerState = { connected: false, connecting: false, talking: false, muted: true, message: '' };
  private readonly device: string;
  private readonly loadSDK: () => Promise<MediaSDK>;
  private readonly credentials: (signal: AbortSignal) => Promise<Credential>;
  private readonly changed: (state: PlayerState) => void;
  constructor(device: string, loadSDK: () => Promise<MediaSDK>, credentials: (signal: AbortSignal) => Promise<Credential>, changed: (state: PlayerState) => void) {
    this.device = device; this.loadSDK = loadSDK; this.credentials = credentials; this.changed = changed;
  }
  private emit(patch: Partial<PlayerState>) { this.state = { ...this.state, ...patch }; this.changed(this.state); }
  async start() {
    if (this.state.connecting) return;
    this.close();
    const generation = this.generation;
    this.controller = new AbortController();
    this.emit({ connecting: true, message: '正在连接设备…' });
    this.timeout = setTimeout(() => { if (generation === this.generation) { this.close(); this.emit({ message: '连接超时，请检查设备和手机网络后重试' }); } }, 20000);
    let connection: Connection | undefined;
    const allocated: Output[] = [];
    let owned = false;
    try {
      const [sdk, credential] = await Promise.all([this.loadSDK(), this.credentials(this.controller.signal)]);
      if (generation !== this.generation) return;
      this.emit({ profile: credential.profiles?.stream });
      sdk.TiRtc.initialize(sdk.TiRtcInitOptions({ appId: credential.app_id }));
      await sdk.TiRtc.videoOutputReady();
      if (generation !== this.generation) return;
      const ids = streamIds(credential.profiles?.stream);
      connection = new sdk.TiRtcConn();
      const audio = sdk.TiRtcAudioOutput({ connection, streamId: ids.upAudio });
      allocated.push(audio);
      const video = sdk.TiRtcVideoOutput({ connection, streamId: ids.upVideo });
      allocated.push(video);
      const input = new sdk.TiRtcAudioInput({ connection, streamId: ids.downAudio });
      allocated.push(input);
      const resources: Resources = { connection, audio, video, input, ids, audioSubscribed: false, videoSubscribed: false };
      this.resources = resources; owned = true;
      input.setOptions({ sampleRate: 8000 });
      await connection.connect({ deviceId: this.device, token: credential.token });
      if (generation !== this.generation) { safely(() => connection?.disconnect()); return; }
      await video.attach();
      if (generation !== this.generation) { safely(() => video.detach()); return; }
      resources.videoSubscribed = true; connection.subscribeVideo({ streamId: ids.upVideo });
      if (!this.state.muted) { audio.attach(); resources.audioSubscribed = true; connection.subscribeAudio({ streamId: ids.upAudio }); }
      this.emit({ connected: true, connecting: false, message: '已连接 · 点击声音按钮开启或关闭声音' });
    } catch {
      if (!owned) { for (const output of allocated) safely(() => output.detach()); if (connection) safely(() => connection?.disconnect()); }
      if (generation !== this.generation) { if (connection) safely(() => connection?.disconnect()); return; }
      this.close(); this.emit({ message: '暂时无法连接，请确认设备在线后重试' });
    } finally { if (generation === this.generation) { clearTimeout(this.timeout); this.emit({ connecting: false }); } }
  }
  toggleMute() {
    const muted = !this.state.muted, r = this.resources;
    if (r && this.state.connected) {
      try {
        if (muted) { if (r.audioSubscribed) r.connection.unsubscribeAudio({ streamId: r.ids.upAudio }); r.audioSubscribed = false; r.audio.detach(); }
        else { r.audio.attach(); r.audioSubscribed = true; r.connection.subscribeAudio({ streamId: r.ids.upAudio }); }
      } catch { this.emit({ message: '声音暂不可用，请重试' }); return; }
    }
    this.emit({ muted });
  }
  async startTalk() {
    const r = this.resources, generation = this.generation;
    if (!r || !this.state.connected || this.state.talking || this.talkPending) return;
    this.talkPending = true; this.emit({ talking: true });
    try {
      await r.input.start();
      if (generation !== this.generation || !this.state.talking) { safely(() => r.input.stop()); return; }
      await r.input.attach();
      if (generation !== this.generation || !this.state.talking) safely(() => r.input.stop());
    } catch { if (generation === this.generation) { this.stopTalk(); this.emit({ message: '麦克风失败' }); } }
    finally { if (generation === this.generation) this.talkPending = false; }
  }
  stopTalk() {
    if (!this.state.talking) return;
    this.emit({ talking: false });
    if (this.resources) safely(() => this.resources?.input.stop());
  }
  close() {
    this.generation++; clearTimeout(this.timeout); this.controller?.abort(); this.controller = undefined;
    this.stopTalk(); this.talkPending = false;
    const r = this.resources; this.resources = undefined;
    if (r) {
      if (r.videoSubscribed) safely(() => r.connection.unsubscribeVideo({ streamId: r.ids.upVideo }));
      if (r.audioSubscribed) safely(() => r.connection.unsubscribeAudio({ streamId: r.ids.upAudio }));
      safely(() => r.audio.detach()); safely(() => r.video.detach()); safely(() => r.input.detach()); safely(() => r.connection.disconnect());
    }
    this.emit({ connected: false, connecting: false, talking: false, message: '连接已结束，请重新连接' });
  }
}
