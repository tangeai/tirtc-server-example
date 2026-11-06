import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
export default defineConfig(({ command }) => ({
  base: command === 'serve' ? '/' : '/static/app/',
  plugins: [react()],
  server: { port: 5174, proxy: { '/v1/ai': 'http://127.0.0.1:9004', '/v1/call': 'http://127.0.0.1:9005', '/v1/voip': 'http://127.0.0.1:9003', '/v1/recognition': 'http://127.0.0.1:9006', '/v1': 'http://127.0.0.1:9002', '/plugin.wasm': 'http://127.0.0.1:9002', '/librender.wasm': 'http://127.0.0.1:9002', '/static/js': 'http://127.0.0.1:9002', '/static/legal': 'http://127.0.0.1:9002' } },
  build: { outDir: '../static/app', emptyOutDir: true },
}));
