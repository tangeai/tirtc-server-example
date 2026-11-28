import { useEffect, useRef, useState } from 'react';
import type { Device } from '../domain/devices';
import { token } from '../services/api';
import { readCardStates, type CardState } from '../services/device-card-states';

export function useDeviceDashboard() {
  const [states, setStates] = useState<Record<string, CardState>>({});
  const generation = useRef(0);
  useEffect(() => () => { generation.current++; }, []);
  function update(rows: Device[]) {
    const batch = ++generation.current, account = token();
    setStates({});
    void readCardStates(rows, () => generation.current === batch && account === token(), (id, value) => {
      setStates(previous => ({ ...previous, [id]: value }));
    });
  }
  return { recover: async (): Promise<Device[] | undefined> => undefined, update, summary: (id: string) => states[id], restored: false };
}
