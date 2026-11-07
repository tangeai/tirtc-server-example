import { createRoot } from 'react-dom/client';
import Auth from './pages/Auth';
import Bind from './pages/Bind';
import Room from './pages/Room';
import Contacts from './pages/Contacts';
import Devices from './pages/Devices';
import Roles from './pages/Roles';
import Player from './pages/Player';
import { UIProvider } from './components/UIProvider';
import { ConfirmProvider } from './components/Confirm';
import { sessionToken } from './lib/api';
import './styles.css';

const path = location.pathname;
const isBind = path === '/bind';
if ((isBind || path === '/room' || path === '/contacts' || path === '/devices' || path === '/agent' || path === '/player') && !sessionToken()) window.MiniProgramPage?.embedded ? window.MiniProgramPage.requireLogin() : location.replace('/login');
else createRoot(document.getElementById('root')!).render(<UIProvider><ConfirmProvider>{isBind ? <Bind/> : path === '/room' ? <Room/> : path === '/contacts' ? <Contacts/> : path === '/devices' ? <Devices/> : path === '/agent' ? <Roles/> : path === '/player' ? <Player/> : <Auth/>}</ConfirmProvider></UIProvider>);
