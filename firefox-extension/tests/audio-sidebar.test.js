import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setupAudio } from '../audio-sidebar.js';

const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };
const tick = () => new Promise(resolve => setImmediate(resolve));

function fixture(t) {
  const globals = ['browser', 'window', 'navigator', 'MediaRecorder', 'MediaStream', 'AudioContext', 'OfflineAudioContext'];
  const descriptors = globals.map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]);
  t.after(() => descriptors.forEach(([key, descriptor]) => {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else delete globalThis[key];
  }));
  const node = () => ({ value: '', hidden: true, disabled: false, dataset: {}, listeners: {},
    addEventListener(type, fn) { this.listeners[type] = fn; }, pause() {}, load() {}, removeAttribute() {},
  });
  const ids = ['record-audio', 'audio-panel', 'audio-help', 'audio-status', 'audio-preview', 'audio-start', 'audio-stop',
    'audio-send', 'audio-cancel', 'audio-result', 'audio-transcript', 'audio-term', 'audio-explain', 'audio-explain-form', 'audio-error', 'audio-close', 'conversation'];
  const ui = Object.fromEntries(ids.map(id => [id, node()]));
  let recorder, stops = 0, captureCalls = 0;
  const capture = deferred();
  const stream = { getAudioTracks: () => [{ addEventListener() {}, stop() { stops++; } }],
    getTracks: () => [{ stop() { stops++; } }, { stop() { stops++; } }],
  };
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: { mediaDevices: { getDisplayMedia() { captureCalls++; return capture.promise; } } } });
  globalThis.browser = { isChrome: true, storage: { local: { get: async () => ({ settings: { aiUrl: 'https://proxy.example', model: 'deep' } }) } } };
  globalThis.window = { addEventListener() {} };
  globalThis.MediaStream = class {};
  globalThis.MediaRecorder = class {
    constructor() { recorder = this; this.mimeType = 'audio/webm'; this.state = 'inactive'; }
    start() { this.state = 'recording'; }
    stop() { this.state = 'inactive'; this.ondataavailable({ data: new Blob(['clip']) }); this.onstop(); }
  };
  globalThis.AudioContext = class { async decodeAudioData() { return { duration: 1 }; } async close() {} };
  globalThis.OfflineAudioContext = class {
    createBufferSource() { return { connect() {}, start() {} }; }
    async startRendering() { return { getChannelData: () => Float32Array.of(0.5, -0.5) }; }
  };
  const state = { id: 'current', settings: { configured: true }, status: 'idle', saveStatus: 'idle' };
  const commands = [];
  const audioUI = setupAudio({ ui, getState: () => state, command: async (type, fields) => { commands.push({ type, fields }); return { ok: true }; } });
  audioUI.update();
  const click = id => ui[id].listeners.click();
  return { ui, click, capture, stream, state, commands, captureCalls: () => captureCalls, stops: () => stops,
    async record() {
      click('record-audio'); capture.resolve(stream); await tick();
      click('audio-stop'); await tick();
      assert.equal(ui['audio-preview'].hidden, false);
      assert.equal(ui['audio-send'].hidden, false);
    },
  };
}

test('recording requires an explicit send, and chosen transcript words start contextual explanations', async t => {
  const app = fixture(t);
  let uploads = 0;
  t.mock.method(globalThis, 'fetch', async () => {
    uploads++; return Response.json({ choices: [{ message: { content: '{"transcript":"A serendipitous encounter."}' } }] });
  });
  await app.record();
  assert.equal(uploads, 0);
  app.click('audio-send'); await tick();
  assert.equal(uploads, 1);
  assert.equal(app.ui['audio-transcript'].value, 'A serendipitous encounter.');
  app.ui['audio-transcript'].selectionStart = 2;
  app.ui['audio-transcript'].selectionEnd = 15;
  app.ui['audio-transcript'].listeners.select();
  assert.equal(app.ui['audio-term'].value, 'serendipitous');
  await app.ui['audio-explain-form'].listeners.submit({ preventDefault() {} });
  assert.deepEqual(app.commands, [{ type: 'AUDIO_EXPLAIN', fields: { conversationId: 'current', term: 'serendipitous', transcript: 'A serendipitous encounter.' } }]);
  app.click('audio-close');
  assert.equal(app.ui['audio-preview'].hidden, true);
  assert.equal(app.ui['audio-transcript'].value, '');
});

test('closing while the sharing picker is pending releases late capture streams', async t => {
  const app = fixture(t);
  app.click('record-audio');
  assert.equal(app.captureCalls(), 1); // Capture invoked synchronously during the click.
  app.click('audio-close');
  app.capture.resolve(app.stream); await tick();
  assert.equal(app.stops(), 2);
  assert.equal(app.ui['audio-preview'].hidden, true);
});

test('an unsupported provider retains the clip for retry; canceled late results stay hidden', async t => {
  const app = fixture(t);
  let calls = 0;
  const delayed = deferred();
  t.mock.method(globalThis, 'fetch', async () => {
    calls++;
    if (calls === 1) return Response.json({ error: { message: 'Unsupported input_audio' } }, { status: 400 });
    return delayed.promise;
  });
  await app.record();
  app.click('audio-send'); await tick();
  assert.match(app.ui['audio-error'].textContent, /CLIProxyAPI/u);
  assert.equal(app.ui['audio-preview'].hidden, false);
  assert.equal(app.ui['audio-send'].disabled, false);
  app.click('audio-send'); await tick();
  app.click('audio-cancel');
  delayed.resolve(Response.json({ choices: [{ message: { content: '{"transcript":"obsolete"}' } }] }));
  await tick();
  assert.equal(app.ui['audio-result'].hidden, true);
  assert.equal(app.ui['audio-transcript'].value, '');
  app.click('audio-close');
});
