import { test } from 'node:test';
import assert from 'node:assert/strict';
import { transcribeAudio } from '../api.js';
import { bytesToBase64, encodeWav, recordStream, recordPlayer } from '../audio.js';

const settings = { aiUrl: 'https://proxy.example/api/v1', aiKey: 'proxy-secret', model: 'deep', thinkingLevel: 'high' };
const audio = { format: 'wav', data: bytesToBase64(encodeWav(Float32Array.of(0, 0.5, -0.5))) };
const reply = transcript => Response.json({ choices: [{ message: { content: JSON.stringify({ transcript }) } }] });

test('WAV encoder preserves PCM samples and sends mono 16 kHz header', () => {
  const bytes = encodeWav(Float32Array.of(-2, -1, 0, 0.5, 1, 2));
  const view = new DataView(bytes.buffer);
  assert.equal(new TextDecoder().decode(bytes.slice(0, 4)), 'RIFF');
  assert.equal(new TextDecoder().decode(bytes.slice(8, 12)), 'WAVE');
  assert.equal(view.getUint32(4, true), bytes.length - 8);
  assert.equal(view.getUint16(22, true), 1);
  assert.equal(view.getUint32(24, true), 16000);
  assert.equal(view.getUint16(34, true), 16);
  assert.equal(view.getUint32(40, true), 12);
  assert.deepEqual(Array.from({ length: 6 }, (_, i) => view.getInt16(44 + i * 2, true)), [-32768, -32768, 0, 16383, 32767, 32767]);
});

test('audio uploads only to the configured CLIProxyAPI chat endpoint with the chosen model', async t => {
  const requests = [];
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    requests.push({ url, options, body: JSON.parse(options.body) });
    return reply('That was serendipitous.');
  });
  assert.equal(await transcribeAudio({ ...settings, audioModel: 'audio-capable' }, audio), 'That was serendipitous.');
  assert.equal(await transcribeAudio(settings, audio), 'That was serendipitous.');
  assert.equal(requests.length, 2);
  for (const { url, options, body } of requests) {
    assert.equal(url, 'https://proxy.example/api/v1/chat/completions');
    assert.equal(options.headers.Authorization, 'Bearer proxy-secret');
    assert.equal(body.stream, false);
    assert.equal(body.reasoning_effort, undefined);
    assert.deepEqual(body.messages[1].content[1], { type: 'input_audio', input_audio: audio });
    assert.equal(JSON.stringify(body).includes('proxy-secret'), false);
  }
  assert.equal(requests[0].body.model, 'audio-capable');
  assert.equal(requests[1].body.model, 'deep');
});

test('unsupported audio reports compatibility error and never falls back or leaks credentials', async t => {
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => {
    calls++;
    return Response.json({ error: { message: 'input_audio unsupported proxy-secret' } }, { status: 400 });
  });
  await assert.rejects(transcribeAudio(settings, audio), error => /CLIProxyAPI.*input_audio/u.test(error.message) && !error.message.includes('proxy-secret'));
  assert.equal(calls, 1);
});

test('invalid, oversized or non-WAV clips are rejected before a network request', async t => {
  t.mock.method(globalThis, 'fetch', () => assert.fail('Invalid audio reached the network'));
  for (const clip of [null, { format: 'mp3', data: 'AAAA' }, { format: 'wav', data: 'bad?' }, { format: 'wav', data: 'A'.repeat(1_400_004) }]) {
    await assert.rejects(transcribeAudio(settings, clip), /WAV/u);
  }
});

test('transcription rejects absent speech or an unsupported model answer instead of inventing words', async t => {
  t.mock.method(globalThis, 'fetch', async () => reply(''));
  await assert.rejects(transcribeAudio(settings, audio), /No intelligible speech/u);
  t.mock.method(globalThis, 'fetch', async () => Response.json({ choices: [{ message: { content: 'I cannot listen to audio.' } }] }));
  await assert.rejects(transcribeAudio(settings, audio), /did not return a transcript/u);
});

test('audio streaming replies and fenced JSON transcripts work through the same route', async t => {
  const content = '```json\n{"transcript":"A café nearby."}\n```';
  t.mock.method(globalThis, 'fetch', async () => new Response(
    `data: ${JSON.stringify({ choices: [{ delta: { content }, finish_reason: 'stop' }] })}\n\ndata: [DONE]\n\n`,
    { headers: { 'content-type': 'text/event-stream' } },
  ));
  assert.equal(await transcribeAudio(settings, audio), 'A café nearby.');
});

test('an in-flight audio upload can be canceled without a fallback request', async t => {
  t.mock.method(globalThis, 'fetch', (_url, { signal }) => new Promise((_resolve, reject) => {
    if (signal.aborted) reject(signal.reason);
    else signal.addEventListener('abort', () => reject(signal.reason), { once: true });
  }));
  const controller = new AbortController();
  const request = transcribeAudio(settings, audio, { signal: controller.signal });
  controller.abort();
  await assert.rejects(request, { name: 'AbortError' });
});

function recorderFixture(t) {
  const originalStream = globalThis.MediaStream;
  const originalRecorder = globalThis.MediaRecorder;
  t.after(() => { globalThis.MediaStream = originalStream; globalThis.MediaRecorder = originalRecorder; });
  let timer, stopped = 0, instance;
  const audioTrack = { addEventListener() {}, stop() { stopped++; } };
  const videoTrack = { stop() { stopped++; } };
  const stream = { getAudioTracks: () => [audioTrack], getTracks: () => [audioTrack, videoTrack] };
  globalThis.MediaStream = class { constructor(tracks) { this.tracks = tracks; } };
  globalThis.MediaRecorder = class {
    constructor(stream) { instance = this; this.stream = stream; this.state = 'inactive'; this.mimeType = 'audio/webm'; }
    start() { this.state = 'recording'; }
    stop() {
      if (this.state === 'inactive') return;
      this.state = 'inactive';
      this.ondataavailable({ data: new Blob(['clip']) });
      this.onstop();
    }
  };
  t.mock.method(globalThis, 'setTimeout', (fn, duration) => { timer = { fn, duration }; return 1; });
  t.mock.method(globalThis, 'clearTimeout', () => {});
  return { stream, timer: () => timer, stopped: () => stopped, instance: () => instance };
}

test('recording contains audio only, stops automatically at 30 seconds and releases all capture tracks', async t => {
  const fixture = recorderFixture(t);
  const recording = recordStream(fixture.stream);
  assert.equal(fixture.instance().stream.tracks.length, 1);
  assert.equal(fixture.timer().duration, 30000);
  fixture.timer().fn();
  assert.equal(await (await recording.finished).text(), 'clip');
  assert.equal(fixture.stopped(), 2);
});

test('canceling a recording discards the clip and stops both audio and video capture', async t => {
  const fixture = recorderFixture(t);
  const recording = recordStream(fixture.stream);
  recording.cancel();
  await assert.rejects(recording.finished, { name: 'AbortError' });
  assert.equal(fixture.stopped(), 2);
});

test('sharing without audio immediately stops capture and explains the missing checkbox', () => {
  let stopped = false;
  assert.throws(() => recordStream({ getAudioTracks: () => [], getTracks: () => [{ stop() { stopped = true; } }] }), /Share tab audio/u);
  assert.equal(stopped, true);
});

test('Firefox navigation interrupts recording; cancel disconnects the player port', async t => {
  const previous = globalThis.browser;
  t.after(() => { globalThis.browser = previous; });
  const event = () => ({ addListener(fn) { this.listener = fn; } });
  const ports = [];
  globalThis.browser = { tabs: { connect(_tabId, options) {
    assert.equal(options.frameId, 7);
    const port = { onMessage: event(), onDisconnect: event(), postMessage() {}, disconnect() { this.disconnected = true; this.onDisconnect.listener(); } };
    ports.push(port); return port;
  } } };
  const navigated = recordPlayer(1, 7);
  ports[0].onDisconnect.listener();
  await assert.rejects(navigated.finished, /disconnected/u);
  const canceled = recordPlayer(1, 7);
  canceled.cancel();
  await assert.rejects(canceled.finished, { name: 'AbortError' });
  assert.equal(ports[1].disconnected, true);
});
