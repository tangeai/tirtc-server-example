export function DeviceIcon({ name }: { name: 'monitor' | 'contacts' | 'role' | 'room' }) {
  return <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    {name === 'monitor' && <><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4"/></>}
    {name === 'contacts' && <><circle cx="9" cy="8" r="3"/><path d="M3 21v-3a6 6 0 0 1 12 0v3M16 5a3 3 0 0 1 0 6M17 15a5 5 0 0 1 4 5"/></>}
    {name === 'role' && <><rect x="4" y="7" width="16" height="13" rx="3"/><path d="M12 3v4M1 12v4M23 12v4M9 16h6"/><path d="M8 11v1M16 11v1"/></>}
    {name === 'room' && <><path d="M14 14H6l-4 4V5a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2z"/><path d="M8 18h10l4 3V10a2 2 0 0 0-2-2"/></>}
  </svg>;
}
