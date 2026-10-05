import { test } from 'node:test';
import assert from 'node:assert/strict';

async function fixture(t, contextMode = 'surrounding') {
  const original = globalThis.browser;
  t.after(() => { globalThis.browser = original; });
  const origin = 'moz-extension://audio-test/';
  const sender = { id: 'audio-test', url: origin + 'sidebar.html' };
  const event = { addListener() {} };
  let listener, state, finish;
  const requests = [], probes = [];
  globalThis.browser = {
    runtime: { id: sender.id, getURL: path => origin + path, onConnect: event,
      onMessage: { addListener(fn) { listener = fn; } },
      async sendMessage(message) { state = structuredClone(message.state); if (state.status === 'ready') finish?.(); },
    },
    storage: { local: { get: async () => ({ settings: { aiUrl: 'https://proxy.example', model: 'deep', contextMode } }) }, onChanged: event },
    menus: { create() {}, onClicked: event }, browserAction: { onClicked: event }, commands: { onCommand: event },
    windows: { onRemoved: event }, webNavigation: { getAllFrames: async () => [{ frameId: 0 }, { frameId: 7 }, { frameId: 9 }] },
    tabs: { query: async options => { assert.equal(options.windowId, 1); return [{ id: 23 }]; },
      sendMessage: async (tabId, message, options) => {
        probes.push({ tabId, message, options });
        if (options.frameId === 9) throw new Error('Frame inaccessible');
        return { available: true, score: options.frameId === 7 ? 1000 : 10 };
      },
    },
  };
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    requests.push({ url, body: JSON.parse(options.body) });
    return Response.json({ choices: [{ message: { content: 'Serendipitous means happening by fortunate chance.' } }] });
  });
  await import(`../background.js?audio-controller=${crypto.randomUUID()}`);
  const send = (type, fields = {}, from = sender) => listener({ type, windowId: 1, conversationId: state?.id, ...fields }, from);
  state = structuredClone((await send('SIDEBAR_GET')).state);
  return { send, state: () => state, requests, probes,
    async explain(term, transcript) {
      const done = new Promise(resolve => { finish = resolve; });
      const result = await send('AUDIO_EXPLAIN', { term, transcript });
      assert.equal(result.ok, true, result.error);
      await done;
    },
  };
}

test('a word chosen from audio reuses the explanation flow with transcript context', async t => {
  const app = await fixture(t);
  await app.explain('serendipitous', 'It was a serendipitous encounter.');
  assert.equal(app.state().selection.term, 'serendipitous');
  assert.equal(app.state().selection.context, 'It was a serendipitous encounter.');
  assert.equal(app.state().saveStatus, 'idle');
  assert.equal(app.requests.length, 1);
  assert.match(app.requests[0].body.messages[0].content, /serendipitous encounter/u);
  // The audio source must not trigger DOM selection recapture or a page popup message.
  assert.deepEqual(app.probes, []);
  await app.explain('encounter', 'It was a serendipitous encounter.');
  assert.equal(app.state().selection.term, 'encounter');
});

test('Deep no-context preference excludes transcript from the explanation', async t => {
  const app = await fixture(t, 'none');
  await app.explain('encounter', 'A private sentence around the word.');
  assert.equal(app.state().selection.context, '');
  assert.equal(JSON.stringify(app.requests).includes('A private sentence'), false);
});

test('Firefox locates a playing embedded player and skips inaccessible frames', async t => {
  const app = await fixture(t);
  const result = await app.send('AUDIO_SOURCE');
  assert.deepEqual(result, { ok: true, tabId: 23, frameId: 7 });
  assert.equal(app.probes.length, 3);
  assert.ok(app.probes.every(item => item.message.type === 'AUDIO_PROBE'));
});

test('page scripts cannot invoke audio actions and stale selections cannot replace a conversation', async t => {
  const app = await fixture(t);
  const forged = await app.send('AUDIO_EXPLAIN', { term: 'encounter', transcript: 'An encounter.' },
    { id: 'audio-test', url: 'https://page.example', tab: { id: 23, windowId: 1 } });
  assert.equal(forged.ok, false);
  assert.match(forged.error, /only available.*sidebar/u);
  const stale = await app.send('AUDIO_EXPLAIN', { conversationId: 'old', term: 'encounter', transcript: 'An encounter.' });
  assert.equal(stale.ok, false);
  assert.match(stale.error, /selected word has changed/u);
  assert.equal(app.requests.length, 0);
});
