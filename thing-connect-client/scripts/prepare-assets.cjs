const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');
const source = path.resolve(root, '../thing-connect');
const out = path.join(root, 'src/generated');
fs.mkdirSync(out, { recursive: true });

for (const name of ['terms', 'privacy']) {
  const html = fs.readFileSync(path.join(source, 'user-server/static/legal', name + '.html'), 'utf8');
  const body = html.match(/<main\b[^>]*>([\s\S]*?)<\/main>/i)?.[1];
  if (!body) throw new Error('Legal document has no main content: ' + name);
  fs.writeFileSync(path.join(out, name + '.json'), JSON.stringify(body));
}

// Embed the auth logo so native previews do not depend on a separate image URL.
const logo = fs.readFileSync(path.join(root, 'src/assets/auth/xiaotai-green-small.png'));
if (logo.length > 64 * 1024) throw new Error('Auth logo must stay below 64 KiB; use the small transparent PNG.');
fs.writeFileSync(path.join(out, 'auth-logo.json'), JSON.stringify('data:image/png;base64,' + logo.toString('base64')));
