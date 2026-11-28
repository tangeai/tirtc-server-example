import { useEffect, useRef, useState } from 'react';
import type { Device } from '../domain/devices';
import { token } from '../services/api';
import { deviceRevision } from '../services/device-events';
import { readSummaries } from '../../../thing-connect/user-server/user-web/src/lib/device-summaries';
import { roleSummary, type Summary } from '../../../thing-connect/user-server/user-web/src/lib/devices';
import { t } from '../../../thing-connect/user-server/user-web/src/lib/messages';
import { sha256 } from 'js-sha256';
import { restoreSnapshot, markAway, saveSnapshot } from '../../../thing-connect/user-server/user-web/src/lib/snapshot';

type Snapshot = { devices: Device[]; summaries: Record<string, Summary> };
export function useDeviceDashboard() {
  const [summaries, setSummaries] = useState<Record<string, Summary>>({});
  const [restored, setRestored] = useState(false);
  const snapshot = useRef<Snapshot>({ devices: [], summaries: {} });
  const key = useRef(''), generation = useRef(0), owner = useRef(''), changed = useRef(-1);
  useEffect(() => {
    const away = () => {
      if (key.current && owner.current === token() && changed.current === deviceRevision()) {
        saveSnapshot(key.current, snapshot.current); markAway(key.current);
      }
    };
    window.addEventListener('pagehide', away);
    return () => { away(); generation.current++; window.removeEventListener('pagehide', away); };
  }, []);
  async function recover() {
    const account = token();
    try {
      // HTTP development origins lack SubtleCrypto. Keep the same session digest on both transports.
      const name = 'device-list-v1:react:' + sha256(account) + ':console';
      if (account !== token()) return;
      key.current = name;
      const saved = restoreSnapshot<Snapshot>(name);
      if (saved && Array.isArray(saved.devices) && saved.summaries) {
        owner.current = account; changed.current = deviceRevision(); snapshot.current = saved;
        setSummaries(saved.summaries); setRestored(true); return saved.devices;
      }
    } catch { /* Browser tab storage is optional. */ }
  }
  function update(rows: Device[]) {
    const revision = ++generation.current, account = token();
    owner.current = account; changed.current = deviceRevision();
    const devices = rows.map(({ device_id, device_name, status, online, profiles, hardware, mac, firmware_version, bind_time }) =>
      ({ device_id, device_name, status, online, profiles, hardware, mac, firmware_version, bind_time }));
    snapshot.current = { devices, summaries: {} }; setSummaries({}); setRestored(false);
    void readSummaries(devices, () => revision === generation.current && account === token(), (id, value) => {
      snapshot.current.summaries = { ...snapshot.current.summaries, [id]: value };
      setSummaries(snapshot.current.summaries);
    });
  }
  function summary(id: string) {
    const item = summaries[id];
    if (!item) return undefined;
    const recognition = !item.recognition ? t('识别状态待更新') : item.recognition.enabled
      ? t('识别已开启 · {p0} 项', { p0: item.recognition.count }) : t('识别已关闭');
    const role = item.role?.binding.role_id && item.role.name ? item.role.name : roleSummary(item.role);
    const room = item.room;
    const roomLabel = room?.desired_state === 'joined' && room.state === 'joined' && room.room_code
      ? t('房间号 {p0}', { p0: room.room_code }) : undefined;
    return { role, recognition, room: roomLabel };
  }
  return { recover, update, summary, restored };
}
