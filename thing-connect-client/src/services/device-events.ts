let revision = 0;
export const deviceRevision = () => revision;
export function devicesChanged() { revision++; }
