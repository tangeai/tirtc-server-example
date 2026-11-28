import { useEffect, useRef, useState } from 'react';
import type { Device } from '../domain/devices';
import { token } from '../services/api';
import { deviceRevision } from '../services/device-events';
import { readCardStates, type CardState } from '../services/device-card-states';
import { sha256 } from 'js-sha256';
import { restoreSnapshot, markAway, saveSnapshot } from '../../../thing-connect/user-server/user-web/src/lib/snapshot';

type Snapshot = { devices: Device[]; summaries: Record<string, CardState> };
export function useDeviceDashboard() {
  const [summaries, setSummaries] = useState<Record<string, CardState>>({});
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
      const name = 'device-list-v1:react:' + sha256(account) + ':console-cards-v2';
      if (account !== token()) return;
      key.current = name;
      const saved = restoreSnapshot<Snapshot>(name);
      if (saved && Array.isArray(saved.devices) && saved.summaries) {
        owner.current = account; changed.current = deviceRevision(); snapshot.current = saved;
        setSummaries(saved.summaries); return saved.devices;
      }
    } catch { /* Browser tab storage is optional. */ }
  }
  function update(rows: Device[]) {
    const revision = ++generation.current, account = token();
    owner.current = account; changed.current = deviceRevision();
    const devices = rows.map(({ device_id, device_name, status, online, profiles, hardware, mac, firmware_version, bind_time }) =>
      ({ device_id, device_name, status, online, profiles, hardware, mac, firmware_version, bind_time }));
    snapshot.current = { devices, summaries: {} }; setSummaries({});
    void readCardStates(devices, () => revision === generation.current && account === token(), (id, value) => {
      snapshot.current.summaries = { ...snapshot.current.summaries, [id]: value };
      setSummaries(snapshot.current.summaries);
    });
  }
  return { recover, update, summary: (id: string) => summaries[id] };
}
