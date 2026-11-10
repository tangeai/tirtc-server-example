import { useEffect, useRef, useState } from 'react';
import { useLoad } from '@tarojs/taro';
import { ApiError, errorText } from '../services/api';
import { readDevices } from '../services/device-directory';
import type { Device } from '../domain/devices';

// A page owns its requests: no onShow refresh, no overlap, no late UI writes.
export function useDeviceWorkspace<T>(read: (device: string) => Promise<T>) {
  const [device, setDevice] = useState<Device>();
  const [value, setValue] = useState<T>();
  const [busy, setBusy] = useState(false), [error, setError] = useState('');
  const id = useRef(''), alive = useRef(true), locked = useRef(false);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  async function run(operation: () => Promise<void>) {
    if (locked.current || !alive.current) return;
    locked.current = true; setBusy(true); setError('');
    try { await operation(); }
    catch (e) { if (alive.current) setError(errorText(e)); }
    finally { locked.current = false; if (alive.current) setBusy(false); }
  }
  function commit(result: T) { if (alive.current) setValue(result); }
  async function refresh(fresh = true) {
    await run(async () => {
      if (!id.current) throw new ApiError(-1, '请从设备列表打开此页面');
      const rows = await readDevices(fresh);
      const owned = rows.find(row => row.device_id === id.current && row.status === 1);
      if (!owned) {
        if (alive.current) { setDevice(undefined); setValue(undefined); }
        throw new ApiError(40300, '设备不属于当前账号，请返回设备列表');
      }
      if (!alive.current) return;
      setDevice(owned);
      commit(await read(id.current));
    });
  }
  useLoad(options => { id.current = options.device_id || ''; void refresh(false); });
  return { device, value, busy, error, setError, refresh, run, commit, alive };
}
