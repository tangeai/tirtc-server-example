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
  // RichText has its own native rendering tree: apply typography on nodes so
  // headings and paragraphs match in WeChat as well as in the H5 preview.
  const styles = {
    h1: 'font-size:1.3em;line-height:1.4;margin:0 0 0.8em;font-weight:600;',
    h2: 'font-size:1.1em;line-height:1.5;margin:1.2em 0 0.5em;font-weight:600;',
    p: 'font-size:1em;line-height:1.75;margin:0 0 0.8em;word-break:break-word;color:#343d37;',
    ul: 'margin:0.6em 0;padding-left:1.4em;',
    ol: 'margin:0.6em 0;padding-left:1.4em;',
    a: 'color:#078544;word-break:break-word;',
    li: 'font-size:1em;line-height:1.75;margin-bottom:0.4em;',
    footer: 'font-size:0.9em;line-height:1.6;margin-top:1.2em;color:#758078;',
  };
  const styled = body.replace(/<(h1|h2|p|ul|ol|a|li|footer)\b([^>]*)>/gi,
    (_, tag, attrs) => `<${tag}${attrs} style="${styles[tag.toLowerCase()]}">`);
  fs.writeFileSync(path.join(out, name + '.json'), JSON.stringify(styled.replace(/<nav\b([^>]*)>([\s\S]*?)<\/nav>/gi, (_, attrs, content) => `<nav${attrs}>${content.replace(/style="/g, 'style="display:block;margin-bottom:0.35em;')}</nav>`)));
}

// Embed the auth logo so native previews do not depend on a separate image URL.
const logo = fs.readFileSync(path.join(root, 'src/assets/auth/xiaotai-green-small.png'));
if (logo.length > 64 * 1024) throw new Error('Auth logo must stay below 64 KiB; use the small transparent PNG.');
fs.writeFileSync(path.join(out, 'auth-logo.json'), JSON.stringify('data:image/png;base64,' + logo.toString('base64')));
