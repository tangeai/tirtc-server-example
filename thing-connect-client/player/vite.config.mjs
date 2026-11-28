import { fileURLToPath } from 'node:url';
import { readFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { defineConfig } from 'vite';

// Both entries load the same fixed SDK and root WASM URLs across rollbacks.
const manifest = readFileSync(new URL('./sdk.sha256', import.meta.url), 'utf8');
for (const line of manifest.trim().split('\n')) {
  const [expected, name] = line.split(/\s+/);
  const asset = new URL('../../thing-connect/user-server/static/js/' + name, import.meta.url);
  if (createHash('sha256').update(readFileSync(asset)).digest('hex') !== expected) {
    throw new Error('Player SDK/WASM digest mismatch: ' + name);
  }
}

export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  base: '/static/player/',
  esbuild: { jsx: 'automatic' },
  build: {
    outDir: fileURLToPath(new URL('../../thing-connect/user-server/static/player/', import.meta.url)),
    emptyOutDir: false,
  },
});
