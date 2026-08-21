import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
export default defineConfig({
  base: '/admin/',
  plugins: [react()],
  server: { port: 5173, proxy: { '/v1': 'http://127.0.0.1:9000' } },
  build: { outDir: 'dist' },
});
