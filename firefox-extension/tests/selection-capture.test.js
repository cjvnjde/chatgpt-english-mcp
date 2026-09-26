import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';

const contentSource = readFileSync(new URL('../content.js', import.meta.url), 'utf8');

function contentFrame({ privateField = false, collapsed = false, focused = true, sendMessage = async () => {} } = {}) {
  let listener;
  const handlers = {};
  const element = { nodeType: 1, tagName: 'P', closest: () => privateField ? element : null };
  const text = { nodeType: 3, parentElement: element, isConnected: true };
  const range = { startContainer: text, endContainer: text, commonAncestorContainer: element,
    startOffset: 0, endOffset: 4, toString: () => 'bank', cloneRange() { return this; } };
  const selection = { rangeCount: 1, isCollapsed: collapsed, anchorNode: text, focusNode: text,
    getRangeAt: () => range, toString: () => 'bank' };
  const document = { activeElement: element, title: 'Reading', hasFocus: () => focused,
    addEventListener(name, fn) { handlers[name] = fn; } };
  runInNewContext(contentSource, {
    document, Node: { ELEMENT_NODE: 1 }, crypto, location: { href: 'https://reading.example/' },
    window: { getSelection: () => selection, addEventListener() {} }, clearTimeout,
    browser: { runtime: { sendMessage, onMessage: { addListener(fn) { listener = fn; } } } },
  });
  return { capture: message => listener({ type: 'CAPTURE_SELECTION', contextMode: 'none', ...message }), document, handlers };
}

test('content capture distinguishes private fields from unavailable selections', async () => {
  for (const collapsed of [false, true]) {
    const frame = contentFrame({ privateField: true, collapsed });
    for (const useCachedSelection of [false, true]) {
      assert.equal((await frame.capture({ useCachedSelection }))?.privacyDenied, true);
    }
  }
  assert.equal(await contentFrame({ collapsed: true }).capture({ useCachedSelection: false }), null);
});

test('focused frames report identity only, never selected text', async () => {
  const messages = [];
  const frame = contentFrame({ sendMessage: async message => { messages.push(message); } });
  assert.deepEqual(structuredClone(messages), [{ type: 'SELECTION_FRAME_FOCUSED' }]);
  frame.document.activeElement = { tagName: 'IFRAME' };
  frame.handlers.focusin();
  assert.equal(messages.length, 1, 'ancestor document must not claim focus owned by a child frame');
  contentFrame({ focused: false, sendMessage: async message => { messages.push(message); } });
  assert.equal(messages.length, 1);
});

async function background(t, capture) {
  const origin = 'moz-extension://selection-test/';
  const id = 'dictionary-test';
  const page = { id, url: 'https://reading.example/', tab: { id: 7, windowId: 1 }, frameId: 4 };
  const events = {};
  const event = name => ({ addListener(fn) { events[name] = fn; } });
  let state;
  const original = globalThis.browser;
  t.after(() => { globalThis.browser = original; });
  globalThis.browser = {
    runtime: { id, getURL: path => origin + path, onMessage: event('message'), onConnect: event('connect'),
      async sendMessage(message) { state = structuredClone(message.state); } },
    storage: { local: { async get() { return { settings: { aiUrl: 'https://ai.example', model: 'tutor', contextMode: 'none' } }; } }, onChanged: event('storage') },
    menus: { create() {}, onClicked: event('menu') }, browserAction: { onClicked: event('toolbar') },
    commands: { onCommand: event('command') }, windows: { onRemoved: event('windowRemoved') },
    tabs: { async query() { return [page.tab]; }, sendMessage: capture, onRemoved: event('tabRemoved'), onUpdated: event('tabUpdated') },
    sidebarAction: { async isOpen() { return true; }, async open() {}, async toggle() {} },
  };
  const requests = [];
  t.mock.method(globalThis, 'fetch', async (_url, options) => {
    requests.push(JSON.parse(options.body));
    return Response.json({ choices: [{ message: { content: 'River meaning.' } }] });
  });
  await import(`../background.js?capture=${crypto.randomUUID()}`);
  const settle = async () => { for (let n = 0; n < 10; n++) await new Promise(resolve => setImmediate(resolve)); };
  return { events, page, settle, requests, state: () => state };
}

for (const editable of [true, false]) {
  test(`context-menu cannot bypass a private capture (editable flag ${editable})`, async t => {
    const frame = contentFrame({ privateField: true });
    const app = await background(t, async (_id, message) => frame.capture(message));
    app.events.menu({ menuItemId: 'english-dictionary-explain', frameId: 4, selectionText: 'private draft', editable }, app.page.tab);
    await app.settle();
    assert.deepEqual(app.requests, []);
    assert.match(app.state().error, /editable|private/i);
  });
}

for (const action of ['command', 'toolbar']) {
  test(`${action} uses the focused iframe and clears frame identity on navigation`, async t => {
    const frames = [];
    const frame = contentFrame();
    const app = await background(t, async (_id, message, options) => {
      if (message.type !== 'CAPTURE_SELECTION') return;
      frames.push(options.frameId);
      return options.frameId === 4 ? frame.capture(message) : null;
    });
    await app.events.message({ type: 'SELECTION_FRAME_FOCUSED' }, app.page);
    app.events[action]('explain-selection');
    await app.settle();
    assert.equal(app.state().selection?.term, 'bank');
    assert.deepEqual(frames, [4]);
    assert.equal(app.requests.length, 1);
    app.events.tabUpdated(7, { status: 'loading' });
    app.events.command('explain-selection');
    await app.settle();
    assert.equal(frames.at(-1), 0);
  });
}
