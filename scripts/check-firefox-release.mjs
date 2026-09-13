import { createHmac, randomUUID } from 'node:crypto';
import { appendFile, readFile } from 'node:fs/promises';

const manifest = JSON.parse(await readFile(new URL('../firefox-extension/manifest.json', import.meta.url), 'utf8'));
const id = manifest.browser_specific_settings?.gecko?.id;
const version = manifest.version;
const issuer = process.env.AMO_JWT_ISSUER;
const secret = process.env.AMO_JWT_SECRET;
const api = new URL('https://addons.mozilla.org/api/v5/');

function authorization() {
  const now = Math.floor(Date.now() / 1000);
  const encode = value => Buffer.from(JSON.stringify(value)).toString('base64url');
  const token = `${encode({ alg: 'HS256', typ: 'JWT' })}.${encode({ iss: issuer, jti: randomUUID(), iat: now, exp: now + 60 })}`;
  return `JWT ${token}.${createHmac('sha256', secret).update(token).digest('base64url')}`;
}

async function request(url) {
  const response = await fetch(url, {
    headers: { Authorization: authorization(), Accept: 'application/json' },
    redirect: 'error',
    signal: AbortSignal.timeout(30_000),
  });
  if (!response.ok) throw new Error(`AMO version check failed (HTTP ${response.status}). No signing attempted.`);
  return response.json();
}

async function main() {
  if (typeof id !== 'string' || !id || typeof version !== 'string' || !version || /[\r\n]/u.test(version)) {
    throw new Error('The manifest must contain a Gecko ID and a valid version.');
  }
  if (!issuer || !secret) throw new Error('Configure AMO_JWT_ISSUER and AMO_JWT_SECRET in GitHub Actions secrets.');
  const addonURL = new URL(`addons/addon/${encodeURIComponent(id)}/`, api);
  const addon = await request(addonURL);
  if (addon.guid !== id) throw new Error('The AMO add-on ID does not match the manifest.');
  const versionsURL = new URL('versions/', addonURL);
  let url = new URL('?filter=all_with_unlisted&page_size=50', versionsURL);
  const visited = new Set();
  let exists = false;
  while (url) {
    if (url.origin !== api.origin || url.pathname !== versionsURL.pathname || url.username || url.password || visited.has(url.href)) {
      throw new Error('AMO returned invalid version pagination.');
    }
    visited.add(url.href);
    const page = await request(url);
    if (!Array.isArray(page.results) || page.results.some(item => typeof item?.version !== 'string') || !(page.next === null || typeof page.next === 'string')) {
      throw new Error('AMO returned an invalid version list.');
    }
    if (page.results.some(item => item.version === version)) { exists = true; break; }
    url = page.next ? new URL(page.next, versionsURL) : null;
  }
  if (process.env.GITHUB_OUTPUT) await appendFile(process.env.GITHUB_OUTPUT, `sign=${!exists}\nversion=${version}\n`);
  console.log(exists ? `Version ${version} already exists on AMO; skipping signing.` : `Version ${version} is new; sign for unlisted distribution.`);
}

main().catch(error => {
  console.error(error.message);
  process.exitCode = 1;
});
