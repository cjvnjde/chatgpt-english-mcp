import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

function event() {
  const listeners = [];
  return { listeners, addListener(fn) { listeners.push(fn); }, removeListener(fn) {
    listeners.splice(listeners.indexOf(fn), 1);
  }, emit(...args) { return listeners.map(fn => fn(...args)); } };
}

async function chromeFixture() {
  const calls = [];
  const chrome = {
    runtime: { id: 'chrome-test', getURL: path => `chrome-extension://chrome-test/${path}`,
      onMessage: event(), onConnect: event(), onInstalled: event(),
      sendMessage: async () => {}, getPlatformInfo: async () => ({}),
    },
    sidePanel: { onOpened: event(), onClosed: event(),
      open: options => { calls.push(['open', options]); return Promise.resolve(); },
      close: options => { calls.push(['close', options]); return Promise.resolve(); },
    },
    contextMenus: { onClicked: event(),
      create: options => calls.push(['menu', options]), removeAll: callback => callback(),
    },
    windows: { WINDOW_ID_CURRENT: -2, onRemoved: event() },
    tabs: { onRemoved: event(), onUpdated: event(), query: async () => [{ id: 7, windowId: 1 }],
      sendMessage: async () => ({ term: 'bank', sourceId: 'source', context: 'By the river.' }),
    },
    action: { onClicked: event() }, commands: { onCommand: event() },
    storage: { onChanged: event(), local: { get: async () => ({ settings: { aiUrl: 'https://ai.example', model: 'tutor' } }) },
      session: { get: async () => ({}), set: async () => {} },
    },
  };
  const context = vm.createContext({ chrome });
  vm.runInContext(await readFile(new URL('../chrome-api.js', import.meta.url), 'utf8'), context);
  return { chrome, browser: context.browser, calls };
}

test('Chrome messaging bridges promises and leaves broadcasts unclaimed', async () => {
  const { chrome, browser } = await chromeFixture();
  browser.runtime.onMessage.addListener(message => message.type === 'capture'
    ? Promise.resolve({ term: 'bank' }) : undefined);
  const [listener] = chrome.runtime.onMessage.listeners;
  let result;
  assert.equal(listener({ type: 'capture' }, {}, response => { result = response; }), true);
  await Promise.resolve();
  assert.deepEqual(result, { term: 'bank' });
  assert.equal(listener({ type: 'STATE_UPDATED' }, {}, () => assert.fail('Unexpected response')), false);
});

test('native panel opens during gesture and tracks close/toggle separately per window', async () => {
  const { chrome, browser, calls } = await chromeFixture();
  const opening = browser.sidebarAction.open({ windowId: 1 });
  assert.equal(calls[0][0], 'open'); // No asynchronous work before sidePanel.open.
  await opening;
  assert.equal(await browser.sidebarAction.isOpen({ windowId: 1 }), true);
  assert.equal(await browser.sidebarAction.isOpen({ windowId: 2 }), false);
  await browser.sidebarAction.toggle({ windowId: 1 });
  assert.equal(calls[1][0], 'close');
  chrome.sidePanel.onOpened.emit({ windowId: 2 });
  chrome.sidePanel.onClosed.emit({ windowId: 2 });
  assert.equal(await browser.sidebarAction.isOpen({ windowId: 2 }), false);
});

test('context menu is created on install, not every worker wake-up', async () => {
  const { chrome, browser, calls } = await chromeFixture();
  browser.menus.create({ id: 'explain', contexts: ['selection'] });
  assert.equal(calls.length, 0);
  chrome.runtime.onInstalled.emit();
  assert.equal(calls[0][0], 'menu');
});

test('cold worker registers listeners before storage resolves and restores interrupted state', async t => {
  const { chrome, browser, calls } = await chromeFixture();
  let release;
  chrome.storage.local.get = () => new Promise(resolve => { release = resolve; });
  const snapshot = {
    revision: 4, id: 'old', selection: { term: 'bank' }, contextMode: 'none',
    messages: [], lookupStatus: 'loading', status: 'loading', saveStatus: 'saving',
  };
  chrome.storage.session.get = async () => ({ conversations: { 1: { state: snapshot } } });
  const original = globalThis.browser;
  globalThis.browser = browser;
  t.after(() => { globalThis.browser = original; });
  t.mock.method(globalThis, 'setInterval', () => 1);
  t.mock.method(globalThis, 'setTimeout', () => 1);
  await import(`../../firefox-extension/background.js?chrome=${Date.now()}`);
  assert.equal(chrome.runtime.onMessage.listeners.length, 1);
  assert.equal(chrome.action.onClicked.listeners.length, 1);
  chrome.action.onClicked.emit({ id: 7, windowId: 1 });
  assert.equal(calls[0][0], 'open');
  const response = new Promise(resolve => {
    const [claimed] = chrome.runtime.onMessage.emit({ type: 'SIDEBAR_GET', windowId: 1 }, {
      id: chrome.runtime.id, url: chrome.runtime.getURL('sidebar.html'),
    }, resolve);
    assert.equal(claimed, true);
  });
  release({ settings: { aiUrl: 'https://ai.example', model: 'tutor' } });
  const result = await response;
  assert.equal(result.ok, true);
  assert.equal(result.state.selection.term, 'bank');
  assert.equal(result.state.status, 'error');
  assert.equal(result.state.lookupStatus, 'unavailable');
  assert.equal(result.state.saveStatus, 'error');
  assert.match(result.state.saveError, /Check your vocabulary/);

  // Chrome may retain the hidden panel document and its connected port.
  chrome.runtime.onConnect.emit({ name: 'sidebar:1', sender: {
    id: chrome.runtime.id, url: chrome.runtime.getURL('sidebar.html'),
  }, onDisconnect: event() });
  await Promise.resolve();
  chrome.sidePanel.onClosed.emit({ windowId: 1 });
  const requests = [];
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    requests.push(JSON.parse(options.body));
    return Response.json({ choices: [{ message: { content: 'Quick river meaning.' } }] });
  });
  const quick = await new Promise(resolve => chrome.runtime.onMessage.emit({
    type: 'EXPLAIN_SELECTION', requestId: 1,
  }, { id: chrome.runtime.id, url: 'https://reading.example', tab: { id: 7, windowId: 1 }, frameId: 0 }, resolve));
  assert.equal(quick.mode, 'quick');
  assert.equal(quick.text, 'Quick river meaning.');
  assert.equal(requests.length, 1);
});

test('package has MV3 worker, native panel, and adapters before shared scripts', async () => {
  const output = new URL('../dist/english-dictionary-chrome/', import.meta.url);
  const manifest = JSON.parse(await readFile(new URL('manifest.json', output), 'utf8'));
  assert.equal(manifest.manifest_version, 3);
  assert.equal(manifest.minimum_chrome_version, '142');
  assert.deepEqual(manifest.background, { service_worker: 'service-worker.js', type: 'module' });
  assert.equal(manifest.side_panel.default_path, 'sidebar.html');
  assert.ok(manifest.permissions.includes('sidePanel'));
  assert.equal(manifest.browser_specific_settings, undefined);
  assert.deepEqual(manifest.content_scripts[0].js, ['chrome-api.js', 'content.js', 'audio-content.js']);
  assert.match(manifest.content_security_policy.extension_pages, /media-src 'self' blob:/);
  for (const file of ['audio.js', 'audio-sidebar.js', 'audio-content.js']) {
    assert.ok((await readFile(new URL(file, output), 'utf8')).length > 0);
  }
  for (const file of ['sidebar.html', 'options.html']) {
    const html = await readFile(new URL(file, output), 'utf8');
    assert.ok(html.indexOf('chrome-api.js') < html.indexOf('type="module"'));
  }
});
