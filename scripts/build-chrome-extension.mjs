import { cp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
import { execFileSync } from 'node:child_process';

const root = fileURLToPath(new URL('../', import.meta.url));
const source = resolve(root, 'firefox-extension');
const chrome = resolve(root, 'chrome-extension');
const dist = resolve(chrome, 'dist');
const output = resolve(dist, 'english-dictionary-chrome');
const firefox = JSON.parse(await readFile(resolve(source, 'manifest.json'), 'utf8'));
const manifest = {
  manifest_version: 3,
  name: firefox.name,
  version: firefox.version,
  description: firefox.description,
  minimum_chrome_version: '142',
  permissions: ['storage', 'activeTab', 'contextMenus', 'sidePanel'],
  host_permissions: ['http://*/*', 'https://*/*'],
  background: { service_worker: 'service-worker.js', type: 'module' },
  action: firefox.browser_action,
  side_panel: { default_path: 'sidebar.html' },
  options_ui: firefox.options_ui,
  icons: firefox.icons,
  content_scripts: firefox.content_scripts.map(script => ({ ...script, js: ['chrome-api.js', ...script.js] })),
  commands: firefox.commands,
  // Chromium rejects IPv6 host sources in CSP; use localhost for IPv6 loopback services.
  content_security_policy: { extension_pages: firefox.content_security_policy.replace(' http://[::1]:*', '') },
};
await mkdir(dist, { recursive: true });
await rm(output, { recursive: true, force: true });
await mkdir(output);
// Explicit runtime allowlist: no tests, tooling, credentials, or local build output.
for (const file of ['api.js', 'background.js', 'content.js', 'mcp.js', 'prompt.js', 'render.js',
  'settings.js', 'sidebar.js', 'sidebar.css', 'options.js', 'options.css', 'icons']) {
  await cp(resolve(source, file), resolve(output, file), { recursive: true });
}
for (const file of ['sidebar.html', 'options.html']) {
  const html = await readFile(resolve(source, file), 'utf8');
  await writeFile(resolve(output, file), html.replace('<head>', '<head>\n  <script src="chrome-api.js"></script>'));
}
for (const file of ['chrome-api.js', 'service-worker.js']) {
  await cp(resolve(chrome, file), resolve(output, file));
}
await writeFile(resolve(output, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
await cp(resolve(chrome, 'INSTALL.txt'), resolve(output, 'INSTALL.txt'));
const archive = `english-dictionary-chrome-${manifest.version}.zip`;
await rm(resolve(dist, archive), { force: true });
execFileSync('zip', ['-qr', archive, 'english-dictionary-chrome'], { cwd: dist, stdio: 'inherit' });
console.log(`Unpacked extension: ${output}\nRelease ZIP: ${resolve(dist, archive)}`);
