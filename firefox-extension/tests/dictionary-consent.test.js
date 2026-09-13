import { test, mock } from 'node:test';
import assert from 'node:assert/strict';

// Real controller and transports; only browser/network boundaries are substituted.
test('quick/deep routing, cancellation, and explicit AI-selected saving', async t => {
  const origin = 'moz-extension://dictionary-test/';
  const id = 'wordlight@english-mcp.local';
  const sidebar = { id, url: origin + 'sidebar.html' };
  const page = { id, url: 'https://reading.example/story', tab: { id: 7, windowId: 1 }, frameId: 0 };
  let listener;
  let connect;
  let state;
  let opened = false;
  let resolveReady;
  let selection = { term: 'bank', context: 'She sat on the bank of the river.', url: page.url };
  let choice = '{"index":1}';
  let holdQuick = false;
  let quickStarted;
  let holdDeep = false;
  let deepStarted;
  let holdChoice = false;
  let choiceStarted;
  let releaseChoice;
  const writes = [];
  const requests = [];
  const lookups = [];
  const dismissals = [];
  const event = () => ({ addListener() {} });
  const originalBrowser = globalThis.browser;
  globalThis.browser = {
    runtime: {
      id, getURL: path => origin + path,
      onMessage: { addListener(callback) { listener = callback; } },
      onConnect: { addListener(callback) { connect = callback; } },
      async sendMessage(update) {
        state = structuredClone(update.state);
        if (state.status === 'ready') resolveReady?.(state);
      },
    },
    storage: { local: { async get() { return { settings: { aiUrl: 'https://ai.example', model: 'tutor', quickModel: 'fast', mcpUrl: 'https://mcp.example/mcp' } }; } }, onChanged: event() },
    menus: { create() {}, onClicked: event() },
    browserAction: { onClicked: event() }, commands: { onCommand: event() }, windows: { onRemoved: event() },
    tabs: { async sendMessage(_id, message) {
      if (message.type === 'DISMISS_QUICK') { dismissals.push(message); return; }
      return selection;
    } },
    sidebarAction: { async isOpen() { return opened; } },
  };
  t.after(() => { mock.restoreAll(); globalThis.browser = originalBrowser; });
  const held = (signal, started) => new Promise((_resolve, reject) => {
    started?.();
    if (signal.aborted) reject(signal.reason);
    else signal.addEventListener('abort', () => reject(signal.reason), { once: true });
  });
  mock.method(globalThis, 'fetch', async (url, options) => {
    const request = JSON.parse(options.body);
    if (url.startsWith('https://ai.example')) {
      requests.push(request);
      if (request.model === 'fast' && holdQuick) return held(options.signal, quickStarted);
      if (request.model === 'tutor' && holdDeep) return held(options.signal, deepStarted);
      if (request.messages[0].content.startsWith('Select the dictionary sense')) {
        if (holdChoice) await new Promise(resolve => { releaseChoice = resolve; choiceStarted(); });
        return Response.json({ choices: [{ message: { content: choice } }] });
      }
      return Response.json({ choices: [{ message: { content: 'A bank is the land beside a river.' } }] });
    }
    const reply = result => Response.json({ jsonrpc: '2.0', id: request.id, result });
    if (request.method === 'initialize') return reply({ protocolVersion: '2025-03-26', capabilities: { tools: {} } });
    if (request.method === 'notifications/initialized') return new Response(null, { status: 202 });
    if (request.params.name === 'dictionary_lookup') {
      lookups.push(request.params.arguments);
      return reply({ structuredContent: { entries: [{ headword: 'bank', definitions: [{ definition: 'a financial institution' }, { definition: 'land beside a river' }] }] } });
    }
    if (request.params.name === 'vocabulary_save') {
      writes.push(request.params.arguments);
      return reply({ structuredContent: { created: true, itemId: 'saved-river-bank' } });
    }
    throw new Error('Unexpected dictionary operation');
  });
  await import('../background.js');
  const call = (type, fields = {}, sender = sidebar) => listener({ type, windowId: 1, ...fields }, sender);
  const nextAnswer = () => new Promise(resolve => { resolveReady = resolve; });
  let disconnect;
  const open = () => {
    opened = true;
    connect({ sender: sidebar, name: 'sidebar:1', onDisconnect: { addListener(callback) { disconnect = callback; } } });
  };

  const quick = await call('EXPLAIN_SELECTION', {}, page);
  assert.equal(quick.mode, 'quick');
  assert.equal(quick.text, 'A bank is the land beside a river.');
  assert.deepEqual(lookups, []);
  assert.deepEqual(writes, []);
  assert.deepEqual(requests.map(request => request.model), ['fast']);
  assert.equal(requests[0].messages.at(-1).content, "What does 'bank' mean?");

  let answered = nextAnswer();
  open();
  await answered;
  assert.equal(lookups.length, 1);
  assert.deepEqual(requests.map(request => request.model), ['fast', 'tutor']);
  assert.ok(dismissals.length);
  answered = nextAnswer();
  await call('CHAT_SEND', { conversationId: state.id, text: 'Save this in my dictionary now.' });
  await answered;
  assert.deepEqual(writes, []);
  assert.equal((await call('DICTIONARY_SAVE', { conversationId: state.id }, page)).ok, false);
  const conversationId = state.id;
  choice = '{"index":99}';
  await call('DICTIONARY_SAVE', { conversationId });
  assert.equal(state.saveStatus, 'error');
  assert.deepEqual(writes, []);
  choice = '{"index":1}';
  await call('DICTIONARY_SAVE', { conversationId });
  assert.equal(state.saveStatus, 'saved');
  assert.equal(writes.length, 1);
  assert.equal(writes[0].definition, 'land beside a river');
  await call('DICTIONARY_SAVE', { conversationId });
  assert.equal(writes.length, 1);

  const fastCount = requests.filter(request => request.model === 'fast').length;
  answered = nextAnswer();
  assert.equal((await call('EXPLAIN_SELECTION', {}, page)).mode, 'sidebar');
  await answered;
  assert.equal(requests.filter(request => request.model === 'fast').length, fastCount);
  assert.equal((await call('DICTIONARY_SAVE', { conversationId })).ok, false);

  holdChoice = true;
  const choosing = new Promise(resolve => { choiceStarted = resolve; });
  const pendingSave = call('DICTIONARY_SAVE', { conversationId: state.id });
  await choosing;
  answered = nextAnswer();
  await call('EXPLAIN_SELECTION', {}, page);
  await answered;
  releaseChoice();
  await pendingSave;
  assert.equal(writes.length, 1, 'a changed selection must not save the old meaning');
  holdChoice = false;

  opened = false;
  disconnect();
  holdQuick = true;
  const started = new Promise(resolve => { quickStarted = resolve; });
  const pending = call('EXPLAIN_SELECTION', {}, page);
  await started;
  answered = nextAnswer();
  open();
  assert.equal((await pending).mode, 'canceled');
  await answered;
  holdQuick = false;

  holdDeep = true;
  const loading = new Promise(resolve => { deepStarted = resolve; });
  await call('CHAT_SEND', { conversationId: state.id, text: 'Another example?' });
  await loading;
  opened = false;
  disconnect();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(state.status, 'idle', 'closing the sidebar cancels deep generation');
  holdDeep = false;

  // A later selection invalidates an earlier in-flight quick answer.
  holdQuick = true;
  const oldStarted = new Promise(resolve => { quickStarted = resolve; });
  const oldQuick = call('EXPLAIN_SELECTION', {}, page);
  await oldStarted;
  holdQuick = false;
  selection = { term: 'evening', context: 'Good evening.' };
  assert.equal((await call('EXPLAIN_SELECTION', {}, page)).mode, 'quick');
  assert.equal((await oldQuick).mode, 'canceled');
  assert.equal(state.selection.term, 'evening');
  assert.equal(writes.length, 1);
});
