import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { Shell } from '../components/Shell';
import { request } from '../lib/api';
import { useLanguage } from '../lib/i18n';
import { useRemote } from '../lib/use-remote';
import { MediaSession, type Credential, type MediaSDK, type PlayerState, type StreamProfile } from '../media/session';

function presentation(profile?: StreamProfile): { box: CSSProperties; canvas: CSSProperties } {
  const value = profile?.aspect_ratio;
  let ratio = 16 / 9;
  if (typeof value === 'number' && Number.isFinite(value) && value > 0) ratio = value;
  if (typeof value === 'string') {
    const match = /^(\d+):(\d+)$/.exec(value.trim());
    if (match && +match[1] >= 1 && +match[1] <= 9999 && +match[2] >= 1 && +match[2] <= 9999) ratio = +match[1] / +match[2];
    else if (Number.isFinite(Number(value)) && Number(value) > 0) ratio = Number(value);
  }
  const rotation = typeof profile?.camera_rotation === 'number' && [0, 90, 180, 270].includes(profile.camera_rotation) ? profile.camera_rotation : 0;
  const swap = rotation === 90 || rotation === 270, displayRatio = swap ? 1 / ratio : ratio;
  const fit = profile?.object_fit;
  return { box: { aspectRatio: displayRatio, '--display-ratio': displayRatio } as CSSProperties, canvas: { width: swap ? `${100 / displayRatio}%` : '100%', height: swap ? `${100 * displayRatio}%` : '100%', objectFit: fit === 'fill' || fit === 'cover' ? fit : 'contain',
    transform: `translate(-50%, -50%) rotate(${rotation}deg) scaleX(${profile?.hor_mirror === true ? -1 : 1}) scaleY(${profile?.vert_mirror === true ? -1 : 1})` } };
}
export default function Player() {
  const { t } = useLanguage();
  const device = new URLSearchParams(location.search).get('device_id') ?? '';
  const devices = useRemote<{ device_id: string; device_name?: string }[]>('/v1/user/device/list');
  const [state, setState] = useState<PlayerState>({ connected: false, connecting: false, talking: false, muted: true, message: '正在连接设备…' });
  const session = useRef<MediaSession | undefined>(undefined);
  useEffect(() => {
    let alive = true;
    if (!device) { setState(value => ({ ...value, message: '未找到设备，请返回设备列表重新打开' })); return; }
    const loadSDK = async () => { const url = '/static/js/tirtc.es.min.js'; return await import(/* @vite-ignore */ url) as MediaSDK; };
    const media = new MediaSession(device, loadSDK, signal => request<Credential>('/v1/user/device/rtc-token?device_id=' + encodeURIComponent(device), { signal }), value => { if (alive) setState(value); });
    session.current = media;
    const close = () => media.close();
    const blur = () => media.stopTalk();
    const visibility = () => { if (document.hidden) media.stopTalk(); };
    window.addEventListener('pagehide', close); window.addEventListener('blur', blur); document.addEventListener('visibilitychange', visibility);
    void media.start();
    return () => { alive = false; media.close(); window.removeEventListener('pagehide', close); window.removeEventListener('blur', blur); document.removeEventListener('visibilitychange', visibility); };
  }, [device]);
  const style = presentation(state.profile), name = devices.data?.find(item => item.device_id === device)?.device_name;
  return <Shell title="实时查看"><main className="player-layout"><h1>{name || t('未命名设备')} · {device}</h1><section className="player-card" aria-label={t('实时查看')}><div className="player-stage"><div className="canvas-box" style={style.box}><canvas id="canvas" style={style.canvas}/></div></div>
    <div className="player-toolbar"><div className="player-connection"><p role="status">{t(state.message)}</p>{!state.connected && !state.connecting && device && <button onClick={() => void session.current?.start()}>{t('重新连接')}</button>}</div>
    <div className="player-controls"><button type="button" disabled={!state.connected} className={state.talking ? 'talking' : ''} onPointerDown={event => { if (event.button !== 0) return; event.preventDefault(); event.currentTarget.setPointerCapture(event.pointerId); void session.current?.startTalk(); }} onPointerUp={() => session.current?.stopTalk()} onPointerCancel={() => session.current?.stopTalk()} onLostPointerCapture={() => session.current?.stopTalk()} onKeyDown={event => { if ([' ', 'Enter'].includes(event.key) && !event.repeat) { event.preventDefault(); void session.current?.startTalk(); } }} onKeyUp={event => { if ([' ', 'Enter'].includes(event.key)) { event.preventDefault(); session.current?.stopTalk(); } }} onContextMenu={event => event.preventDefault()}>{t(state.talking ? '说话中…' : '按住说话')}</button><button type="button" aria-pressed={!state.muted} onClick={() => session.current?.toggleMute()}>{t(state.muted ? '开启声音' : '关闭声音')}</button></div>
    </div></section>
  </main></Shell>;
}
