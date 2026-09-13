import { createHash, createHmac, randomUUID } from 'node:crypto';
import { appendFile, mkdir, readFile, writeFile } from 'node:fs/promises';

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

async function recoverSignedFile(release) {
  const file = release.file;
  if (release.channel !== 'unlisted' || release.is_disabled || file?.status !== 'public') {
    throw new Error(`Version ${version} exists but is not an approved, enabled unlisted release. Wait for AMO approval or bump the manifest version; no resubmission attempted.`);
  }
  if (typeof file.url !== 'string' || !/^sha256:[a-f0-9]{64}$/u.test(file.hash)) {
    throw new Error('AMO returned invalid signed-file metadata.');
  }
  const url = new URL(file.url);
  if (!['https://addons.mozilla.org', 'https://addons.cdn.mozilla.net'].includes(url.origin) || url.username || url.password) {
    throw new Error('AMO returned an unexpected signed-file download URL.');
  }
  const response = await fetch(url, {
    headers: url.origin === api.origin ? { Authorization: authorization() } : {},
    signal: AbortSignal.timeout(60_000),
  });
  if (!response.ok) throw new Error(`Signed XPI download failed (HTTP ${response.status}).`);
  const bytes = Buffer.from(await response.arrayBuffer());
  if (`sha256:${createHash('sha256').update(bytes).digest('hex')}` !== file.hash) {
    throw new Error('The signed XPI does not match the AMO SHA-256 hash.');
  }
  const directory = new URL('../firefox-extension/dist/', import.meta.url);
  await mkdir(directory, { recursive: true });
  await writeFile(new URL(`english-dictionary-${version}.xpi`, directory), bytes);
}

async function main() {
  if (typeof id !== 'string' || !id || typeof version !== 'string' || !/^[0-9]+(?:\.[0-9]+){0,3}$/u.test(version)) {
    throw new Error('The manifest must contain a Gecko ID and a valid version.');
  }
  if (!issuer || !secret) throw new Error('Configure AMO_JWT_ISSUER and AMO_JWT_SECRET in GitHub Actions secrets.');
  const addonURL = new URL(`addons/addon/${encodeURIComponent(id)}/`, api);
  const addon = await request(addonURL);
  if (addon.guid !== id) throw new Error('The AMO add-on ID does not match the manifest.');
  const versionsURL = new URL('versions/', addonURL);
  let url = new URL('?filter=all_with_unlisted&page_size=50', versionsURL);
  const visited = new Set();
  let existing;
  while (url) {
    if (url.origin !== api.origin || url.pathname !== versionsURL.pathname || url.username || url.password || visited.has(url.href)) {
      throw new Error('AMO returned invalid version pagination.');
    }
    visited.add(url.href);
    const page = await request(url);
    if (!Array.isArray(page.results) || page.results.some(item => typeof item?.version !== 'string') || !(page.next === null || typeof page.next === 'string')) {
      throw new Error('AMO returned an invalid version list.');
    }
    existing = page.results.find(item => item.version === version);
    if (existing) break;
    url = page.next ? new URL(page.next, versionsURL) : null;
  }
  if (existing) await recoverSignedFile(existing);
  if (process.env.GITHUB_OUTPUT) await appendFile(process.env.GITHUB_OUTPUT, `sign=${!existing}\nversion=${version}\n`);
  console.log(existing ? `Recovered the approved signed XPI for ${version}; no resubmission needed.` : `Version ${version} is new; sign for unlisted distribution.`);
}

main().catch(error => {
  console.error(error.message);
  process.exitCode = 1;
});
