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
  let command;
  let toolbarClick;
  let state;
  let opened = false;
  let resolveReady;
  let selection = { term: 'bank', context: 'She sat on the bank of the river. '.repeat(120), url: page.url, sourceId: 'bank-source' };
  let choice = '{"index":1}';
  let holdQuick = false;
  let quickStarted;
  let holdDeep = false;
  let deepStarted;
  let holdChoice = false;
  let choiceStarted;
  let releaseChoice;
  let storageChanged;
  let storedSettings = {
    aiUrl: 'https://ai.example', model: 'tutor', quickModel: 'fast', mcpUrl: 'https://mcp.example/mcp',
    contextMode: 'page', quickContextMode: 'none', thinkingLevel: 'high', quickThinkingLevel: 'low',
    systemPrompt: 'Deep teaching instructions.', quickSystemPrompt: 'Quick teaching instructions.',
  };
  let streamingQuick = false;
  let streamController;
  let resolveUpdate;
  let holdCapture = false;
  let holdSelectionCapture = false;
  let captureStarted;
  let releaseCapture;
  const captures = [];
  const updates = [];
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
    storage: {
      local: { async get() { return { settings: storedSettings }; } },
      onChanged: { addListener(callback) { storageChanged = callback; } },
    },
    menus: { create() {}, onClicked: event() },
    browserAction: { onClicked: { addListener(callback) { toolbarClick = callback; } } },
    commands: { onCommand: { addListener(callback) { command = callback; } } }, windows: { onRemoved: event() },
    tabs: { async query() { return [page.tab]; }, async sendMessage(tabId, message, options) {
      if (message.type === 'DISMISS_QUICK') { dismissals.push(message); return; }
      if (message.type === 'QUICK_UPDATED') {
        updates.push({ tabId, ...message, ...options });
        resolveUpdate?.(message);
        return;
      }
      assert.equal(message.type, 'CAPTURE_SELECTION');
      captures.push(message);
      const captured = selection && {
        ...selection,
        context: message.contextMode === 'none' ? '' : selection.context?.slice(0, message.contextMode === 'page' ? 12000 : 2400),
        url: message.contextMode === 'none' ? '' : selection.url,
      };
      if ((holdCapture && message.expectedTerm) || holdSelectionCapture) {
        captureStarted();
        await new Promise(resolve => { releaseCapture = resolve; });
      }
      return captured;
    } },
    sidebarAction: {
      async isOpen() { return opened; },
      async open() { if (!opened) open(); },
      async toggle() { if (opened) { opened = false; disconnect(); } else open(); },
    },
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
      if (request.model === 'fast' && streamingQuick) {
        return new Response(new ReadableStream({ start(controller) { streamController = controller; } }), { headers: { 'Content-Type': 'text/event-stream' } });
      }
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
  const reference = request => JSON.parse(request.messages[0].content.split('REFERENCE DATA: ')[1]);
  const changeSettings = async update => {
    storedSettings = { ...storedSettings, ...update };
    await storageChanged({ settings: {} }, 'local');
  };
  const sendChunk = content => streamController.enqueue(new TextEncoder().encode(`data: ${JSON.stringify({ choices: [{ index: 0, delta: { content } }] })}\n\n`));
  const finishStream = () => {
    streamController.enqueue(new TextEncoder().encode('data: [DONE]\n\n'));
    streamController.close();
  };
  let disconnect;
  const open = () => {
    opened = true;
    connect({ sender: sidebar, name: 'sidebar:1', onDisconnect: { addListener(callback) { disconnect = callback; } } });
  };

  const quick = await call('EXPLAIN_SELECTION', { requestId: 10 }, page);
  assert.equal(quick.mode, 'quick');
  assert.equal(quick.text, 'A bank is the land beside a river.');
  assert.deepEqual(lookups, []);
  assert.deepEqual(writes, []);
  assert.deepEqual(requests.map(request => request.model), ['fast']);
  assert.equal(requests[0].messages.at(-1).content, "What does 'bank' mean?");
  assert.equal(requests[0].reasoning_effort, 'low');
  assert.ok(requests[0].messages[0].content.startsWith('Quick teaching instructions.'));
  assert.equal(reference(requests[0]).selection.context, '');
  assert.equal(reference(requests[0]).selection.url, '');
  assert.equal(captures[0].contextMode, 'none');
  assert.deepEqual(updates[0], { tabId: 7, frameId: 0, type: 'QUICK_UPDATED', requestId: 10, text: quick.text });

  let answered = nextAnswer();
  open();
  await answered;
  assert.equal(lookups.length, 1);
  assert.deepEqual(requests.map(request => request.model), ['fast', 'tutor']);
  assert.ok(dismissals.length);
  assert.equal(requests[1].reasoning_effort, 'high');
  assert.ok(requests[1].messages[0].content.startsWith('Deep teaching instructions.'));
  assert.equal(reference(requests[1]).selection.context, selection.context);
  assert.equal(captures[1].contextMode, 'page');
  assert.equal(captures[1].sourceId, selection.sourceId);
  answered = nextAnswer();
  await call('CHAT_SEND', { conversationId: state.id, text: 'Save this in my dictionary now.' });
  await answered;
  assert.deepEqual(writes, []);
  assert.equal(requests.at(-1).reasoning_effort, 'high');
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
  assert.equal(requests.at(-1).reasoning_effort, 'high');
  assert.ok(requests.at(-1).messages[0].content.startsWith('Select the dictionary sense'));
  assert.ok(!requests.at(-1).messages[0].content.includes('Deep teaching instructions.'));
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
  selection = { term: 'evening', context: 'Good evening.', sourceId: 'evening-source' };
  assert.equal((await call('EXPLAIN_SELECTION', {}, page)).mode, 'quick');
  assert.equal((await oldQuick).mode, 'canceled');
  assert.equal(state.selection.term, 'evening');
  assert.equal(writes.length, 1);

  // A wider Quick excerpt cannot leak into Deep when its context is disabled.
  await changeSettings({ quickContextMode: 'page', contextMode: 'none' });
  selection = { term: 'wide', context: 'private page context '.repeat(1000), url: page.url, sourceId: 'wide-source' };
  await call('EXPLAIN_SELECTION', { requestId: 20 }, page);
  assert.equal(reference(requests.at(-1)).selection.context.length, 12000);
  answered = nextAnswer();
  open();
  await answered;
  assert.equal(reference(requests.at(-1)).selection.context, '');
  assert.equal(reference(requests.at(-1)).selection.url, '');
  assert.equal(state.contextMode, 'none');
  assert.equal(captures.at(-1).contextMode, 'none');

  // Navigation to a document with the same word must not attach its context.
  opened = false;
  disconnect();
  await changeSettings({ quickContextMode: 'none', contextMode: 'surrounding' });
  selection = { term: 'bank', context: 'original context', sourceId: 'original-document' };
  await call('EXPLAIN_SELECTION', { requestId: 21 }, page);
  selection = { term: 'bank', context: 'wrong document context', sourceId: 'new-document' };
  answered = nextAnswer();
  open();
  await answered;
  assert.equal(reference(requests.at(-1)).selection.term, 'bank');
  assert.equal(reference(requests.at(-1)).selection.context, '');

  // An in-flight recapture cannot overwrite or start a second, newer conversation.
  opened = false;
  disconnect();
  selection = { term: 'old', context: 'old context', sourceId: 'old-source' };
  await call('EXPLAIN_SELECTION', { requestId: 22 }, page);
  holdCapture = true;
  const capturing = new Promise(resolve => { captureStarted = resolve; });
  open();
  await capturing;
  const captureCount = captures.length;
  await call('SIDEBAR_GET');
  assert.equal(captures.length, captureCount, 'sidebar get and connect share one recapture');
  selection = { term: 'new', context: 'new context', sourceId: 'new-source' };
  answered = nextAnswer();
  await call('EXPLAIN_SELECTION', { requestId: 23 }, page);
  await answered;
  const requestCount = requests.length;
  releaseCapture();
  await new Promise(resolve => setImmediate(resolve));
  holdCapture = false;
  assert.equal(requests.length, requestCount);
  assert.equal(state.selection.term, 'new');
  assert.equal(state.selection.context, 'new context');

  // Deliver chunks before completion; a Deep transition suppresses late chunks
  // even when the transport ignores cancellation and continues producing data.
  opened = false;
  disconnect();
  streamingQuick = true;
  let quickFinished = false;
  const liveQuick = call('EXPLAIN_SELECTION', { requestId: 24 }, page).then(result => { quickFinished = true; return result; });
  while (!streamController) await new Promise(resolve => setImmediate(resolve));
  let updated = new Promise(resolve => { resolveUpdate = resolve; });
  sendChunk('A ');
  await updated;
  assert.equal(quickFinished, false);
  assert.equal(updates.at(-1).requestId, 24);
  assert.equal(updates.at(-1).text, 'A ');
  updated = new Promise(resolve => { resolveUpdate = resolve; });
  sendChunk('new meaning');
  await updated;
  assert.equal(updates.at(-1).text, 'A new meaning');
  const updateCount = updates.length;
  answered = nextAnswer();
  open();
  await answered;
  sendChunk(' stale content');
  finishStream();
  assert.equal((await liveQuick).mode, 'canceled');
  assert.equal(updates.length, updateCount);
  assert.equal(writes.length, 1);

  // A truncated stream still delivers text already received before reporting failure.
  opened = false;
  disconnect();
  streamController = null;
  const interruptedQuick = call('EXPLAIN_SELECTION', { requestId: 26 }, page);
  while (!streamController) await new Promise(resolve => setImmediate(resolve));
  const delayedVisibility = mock.method(browser.sidebarAction, 'isOpen', async () => {
    await new Promise(resolve => setImmediate(resolve));
    return opened;
  });
  sendChunk('Useful partial answer');
  streamController.close();
  assert.equal((await interruptedQuick).ok, false);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(updates.at(-1).requestId, 26);
  assert.equal(updates.at(-1).text, 'Useful partial answer');
  delayedVisibility.mock.restore();

  // Settings edited during recapture apply to the next request, not half of this one.
  opened = false;
  disconnect();
  streamingQuick = false;
  await call('EXPLAIN_SELECTION', { requestId: 25 }, page);
  holdCapture = true;
  const snapshotCapture = new Promise(resolve => { captureStarted = resolve; });
  open();
  await snapshotCapture;
  await changeSettings({ systemPrompt: 'Changed teaching instructions.', thinkingLevel: 'minimal', contextMode: 'none' });
  answered = nextAnswer();
  releaseCapture();
  await answered;
  holdCapture = false;
  assert.equal(requests.at(-1).reasoning_effort, 'high');
  assert.ok(requests.at(-1).messages[0].content.startsWith('Deep teaching instructions.'));
  assert.equal(reference(requests.at(-1)).selection.context, 'new context');
  answered = nextAnswer();
  await call('CHAT_SEND', { conversationId: state.id, text: 'Explain it again.' });
  await answered;
  assert.equal(requests.at(-1).reasoning_effort, 'minimal');
  assert.equal(reference(requests.at(-1)).selection.context, '');
  assert.ok(requests.at(-1).messages[0].content.startsWith('Changed teaching instructions.'));

  // Manual input in a fresh sidebar never captures the currently selected page.
  const beforeManual = captures.length;
  const empty = await call('SIDEBAR_GET', { windowId: 2 });
  answered = nextAnswer();
  await call('CHAT_SEND', { windowId: 2, conversationId: empty.state.id, text: 'manual' });
  await answered;
  assert.equal(captures.length, beforeManual);
  assert.deepEqual(reference(requests.at(-1)).selection, { term: 'manual', context: '', title: '', url: '' });

  // Closing from the shortcut must not capture text or start a new model request.
  const beforeShortcut = requests.length;
  command('explain-selection');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(opened, false);
  assert.equal(requests.length, beforeShortcut);

  holdDeep = true;
  const shortcutStarted = new Promise(resolve => { deepStarted = resolve; });
  command('explain-selection');
  await shortcutStarted;
  assert.equal(opened, true);
  const beforeClosing = requests.length;
  command('explain-selection');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(opened, false);
  assert.equal(state.status, 'idle');
  assert.equal(requests.length, beforeClosing);
  holdDeep = false;

  answered = nextAnswer();
  command('explain-selection');
  await answered;
  assert.equal(opened, true);
  toolbarClick();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(opened, true, 'the toolbar remains open-only');

  // Clearing a loading conversation must stay cleared, including after reconnect.
  const active = (await call('SIDEBAR_GET')).state;
  assert.equal((await call('SELECTION_CLEAR', { conversationId: active.id }, page)).ok, false);
  holdDeep = true;
  const clearingResponse = new Promise(resolve => { deepStarted = resolve; });
  await call('CHAT_SEND', { conversationId: active.id, text: 'Give another example.' });
  await clearingResponse;
  const cleared = await call('SELECTION_CLEAR', { conversationId: active.id });
  assert.equal(cleared.ok, true);
  assert.notEqual(cleared.state.id, active.id);
  assert.ok(cleared.state.revision > active.revision);
  await new Promise(resolve => setImmediate(resolve));
  opened = false;
  disconnect();
  open();
  const reset = (await call('SIDEBAR_GET')).state;
  assert.equal(reset.selection, null);
  assert.deepEqual(reset.messages, []);
  assert.equal(reset.lookup, null);
  assert.equal(reset.status, 'idle');
  assert.equal((await call('CHAT_SEND', { conversationId: active.id, text: 'Stale follow-up' })).ok, false);
  assert.equal((await call('DICTIONARY_SAVE', { conversationId: active.id })).ok, false);
  const otherWindow = (await call('SIDEBAR_GET', { windowId: 2 })).state;
  assert.equal(otherWindow.selection.term, 'manual', 'clearing is window-scoped');
  holdDeep = false;
  answered = nextAnswer();
  await call('CHAT_SEND', { conversationId: reset.id, text: 'fresh' });
  await answered;
  assert.equal(reference(requests.at(-1)).selection.term, 'fresh');
  assert.deepEqual(requests.at(-1).messages.slice(1), [{ role: 'user', content: "What does 'fresh' mean?" }]);
  assert.equal((await call('SELECTION_CLEAR', { conversationId: active.id })).ok, false);
  assert.equal((await call('SIDEBAR_GET')).state.selection.term, 'fresh');

  // A selection captured before Clear cannot reappear when its reply arrives.
  holdSelectionCapture = true;
  const beforeClearCapture = new Promise(resolve => { captureStarted = resolve; });
  const pendingSelection = call('EXPLAIN_SELECTION', {}, page);
  await beforeClearCapture;
  await call('SELECTION_CLEAR', { conversationId: state.id });
  releaseCapture();
  assert.equal((await pendingSelection).mode, 'canceled');
  assert.equal((await call('SIDEBAR_GET')).state.selection, null);
  holdSelectionCapture = false;

  // Clear revokes a pending save decision, but does not delete saved vocabulary.
  answered = nextAnswer();
  await call('CHAT_SEND', { conversationId: state.id, text: 'bank' });
  await answered;
  holdChoice = true;
  const clearDuringChoice = new Promise(resolve => { choiceStarted = resolve; });
  const clearedSave = call('DICTIONARY_SAVE', { conversationId: state.id });
  await clearDuringChoice;
  await call('SELECTION_CLEAR', { conversationId: state.id });
  releaseChoice();
  await clearedSave;
  assert.equal(writes.length, 1);
  assert.equal((await call('SIDEBAR_GET')).state.selection, null);
});
