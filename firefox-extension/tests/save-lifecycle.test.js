import { test } from 'node:test';
import assert from 'node:assert/strict';

const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return { promise, resolve };
};

async function controller(t) {
  const origin = 'moz-extension://save-lifecycle/';
  const sender = { id: 'dictionary-test', url: origin + 'sidebar.html' };
  const event = { addListener() {} };
  let listener, state, ready;
  let initialization, preparation, writing;
  const writes = [];
  const originalBrowser = globalThis.browser;
  t.after(() => { globalThis.browser = originalBrowser; });
  globalThis.browser = {
    runtime: {
      id: sender.id, getURL: path => origin + path,
      onMessage: { addListener(fn) { listener = fn; } }, onConnect: event,
      async sendMessage(message) {
        state = structuredClone(message.state);
        if (state.status === 'ready') ready?.resolve();
      },
    },
    storage: { local: { async get() { return { settings: {
      aiUrl: 'https://ai.example', model: 'tutor', mcpUrl: 'https://mcp.example/mcp',
    } }; } }, onChanged: event },
    menus: { create() {}, onClicked: event }, browserAction: { onClicked: event },
    commands: { onCommand: event }, windows: { onRemoved: event }, tabs: {},
  };
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    const request = JSON.parse(options.body);
    if (url.startsWith('https://ai.example')) {
      if (request.messages[0].content.startsWith('Build an editable vocabulary save draft')) {
        const held = preparation;
        if (held) {
          held.started.resolve();
          await held.release.promise; // Deliberately ignore abort: late replies must still be discarded.
          if (held.fail) throw new Error('obsolete preparation failed');
        }
        return Response.json({ choices: [{ message: { content: JSON.stringify({
          index: 1, description: 'River meaning', context: 'Beside a river', notes: [], examples: [], tags: [],
        }) } }] });
      }
      return Response.json({ choices: [{ message: { content: 'An explanation.' } }] });
    }
    const reply = result => Response.json({ jsonrpc: '2.0', id: request.id, result });
    if (request.method === 'initialize') {
      if (initialization) {
        initialization.started.resolve();
        await initialization.release.promise;
      }
      return reply({ protocolVersion: '2025-03-26', capabilities: { tools: {} } });
    }
    if (request.method === 'notifications/initialized') return new Response(null, { status: 202 });
    if (request.params.name === 'dictionary_lookup') return reply({ structuredContent: { entries: [{
      headword: 'bank', definitions: [{ definition: 'a financial institution' }, { definition: 'land beside a river' }],
    }] } });
    writes.push(request.params);
    if (writing) {
      writing.started.resolve();
      await writing.release.promise;
    }
    return reply({ structuredContent: { created: !writing, itemId: 'saved-bank' } });
  });
  await import(`../background.js?save-lifecycle=${crypto.randomUUID()}`);
  const send = (type, fields = {}) => listener({ type, windowId: 1, conversationId: state?.id, ...fields }, sender);
  state = structuredClone((await send('SIDEBAR_GET')).state);
  const answer = async text => {
    ready = deferred();
    const response = await send('CHAT_SEND', { text });
    assert.equal(response.ok, true, response.error);
    await ready.promise;
  };
  await answer('bank');
  return {
    send, answer, writes, state: () => state,
    holdInitialization() { initialization = { started: deferred(), release: deferred() }; return initialization; },
    holdPreparation(fail = false) { preparation = { started: deferred(), release: deferred(), fail }; return preparation; },
    holdWrite() { writing = { started: deferred(), release: deferred() }; return writing; },
  };
}

for (const action of ['clear', 'follow-up']) {
  test(`${action} during MCP initialization prevents a new vocabulary write`, async t => {
    const app = await controller(t);
    await app.send('SAVE_PREPARE');
    const held = app.holdInitialization();
    const saving = app.send('DICTIONARY_SAVE');
    await held.started.promise;
    if (action === 'clear') await app.send('SELECTION_CLEAR');
    else await app.answer('I meant the financial institution.');
    held.release.resolve();
    await saving;
    assert.deepEqual(app.writes, []);
    assert.equal(app.state().saveDraft, null);
  });
}

test('clearing after a dispatched write suppresses the follow-on update without replaying it', async t => {
  const app = await controller(t);
  await app.send('SAVE_PREPARE');
  const held = app.holdWrite();
  const saving = app.send('DICTIONARY_SAVE');
  await held.started.promise;
  await app.send('SELECTION_CLEAR');
  held.release.resolve();
  await saving;
  assert.equal(app.writes.length, 1);
  assert.equal(app.writes[0].name, 'vocabulary_save');
  assert.equal(app.writes[0].arguments.definition, 'land beside a river');
  assert.equal(app.state().selection, null);
  assert.equal(app.state().saveStatus, 'idle');
});

for (const fail of [false, true]) {
  test(`follow-up invalidates a pending save ${fail ? 'failure' : 'draft'}`, async t => {
    const app = await controller(t);
    const held = app.holdPreparation(fail);
    const preparing = app.send('SAVE_PREPARE');
    await held.started.promise;
    await app.answer('I meant the financial institution.');
    held.release.resolve();
    await preparing;
    assert.equal(app.state().saveDraft, null);
    assert.equal(app.state().saveStatus, 'idle');
    assert.equal(app.state().saveError, '');
    assert.deepEqual(app.writes, []);
  });
}

test('cancel discards a late draft; an unchanged save retains its selected definition', async t => {
  const app = await controller(t);
  const held = app.holdPreparation();
  const preparing = app.send('SAVE_PREPARE');
  await held.started.promise;
  await app.send('SAVE_CANCEL');
  held.release.resolve();
  await preparing;
  assert.equal(app.state().saveDraft, null);
  await app.send('SAVE_PREPARE');
  await app.send('DICTIONARY_SAVE');
  assert.equal(app.state().saveStatus, 'saved');
  assert.equal(app.writes.length, 1);
  assert.equal(app.writes[0].arguments.definition, 'land beside a river');
});
