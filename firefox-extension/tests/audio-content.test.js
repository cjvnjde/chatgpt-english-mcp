import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

async function fixture() {
  const event = () => ({ addListener(fn) { this.listener = fn; } });
  let timer, stops = 0, recordingTracks;
  const audioTrack = { addEventListener() {}, stop() { stops++; } };
  const videoTrack = { stop() { stops++; } };
  const stream = { getAudioTracks: () => [audioTrack], getTracks: () => [audioTrack, videoTrack] };
  const player = { paused: false, ended: false, readyState: 4, muted: false, clientWidth: 400, clientHeight: 300, mozCaptureStream: () => stream };
  const browser = { runtime: { id: 'test', getURL: path => 'moz-extension://test/' + path, onMessage: event(), onConnect: event() } };
  const context = vm.createContext({ browser, document: { querySelectorAll: () => [player] }, Blob, Uint8Array, btoa,
    setTimeout(fn, duration) { timer = { fn, duration }; return 1; }, clearTimeout() {},
    MediaStream: class { constructor(tracks) { recordingTracks = tracks; } },
    MediaRecorder: class {
      constructor() { this.state = 'inactive'; this.mimeType = 'audio/ogg'; }
      start() { this.state = 'recording'; }
      stop() { if (this.state === 'inactive') return; this.state = 'inactive'; this.ondataavailable({ data: new Blob(['speech']) }); this.onstop(); }
    },
  });
  vm.runInContext(await readFile(new URL('../audio-content.js', import.meta.url), 'utf8'), context);
  const port = { name: 'audio-recording', sender: { id: 'test', url: 'moz-extension://test/sidebar.html' },
    onMessage: event(), onDisconnect: event(), messages: [], postMessage(message) { this.messages.push(message); },
  };
  return { browser, player, stream, port, timer: () => timer, stops: () => stops, tracks: () => recordingTracks,
    connect() { browser.runtime.onConnect.listener(port); },
  };
}

test('Firefox captures prefixed media audio only, bounds duration, and releases tracks', async () => {
  const app = await fixture();
  const probe = await app.browser.runtime.onMessage.listener({ type: 'AUDIO_PROBE' }, { id: 'test' });
  assert.equal(probe.available, true);
  app.connect(); app.port.onMessage.listener({ type: 'AUDIO_START' });
  assert.equal(app.tracks().length, 1);
  assert.equal(app.timer().duration, 30000);
  app.timer().fn();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(app.stops(), 2);
  assert.equal(app.port.messages[0].type, 'AUDIO_CLIP');
  assert.equal(atob(app.port.messages[0].data), 'speech');
});

test('Firefox disconnect discards recording instead of returning audio after cancellation', async () => {
  const app = await fixture();
  app.connect(); app.port.onMessage.listener({ type: 'AUDIO_START' });
  app.port.onDisconnect.listener();
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(app.stops() >= 2);
  assert.equal(app.port.messages.length, 0);
});

test('untrusted pages cannot open a recording port; unavailable audio gives an actionable error', async () => {
  const app = await fixture();
  app.port.sender.url = 'https://page.example'; app.connect();
  assert.equal(app.port.onMessage.listener, undefined);
  app.port.sender.url = 'moz-extension://test/sidebar.html';
  app.stream.getAudioTracks = () => [];
  app.connect(); app.port.onMessage.listener({ type: 'AUDIO_START' });
  assert.equal(app.port.messages[0].type, 'AUDIO_ERROR');
  assert.match(app.port.messages[0].error, /no capturable audio/u);
  assert.equal(app.stops(), 2);
});
