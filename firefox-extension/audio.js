export const MAX_RECORDING_SECONDS = 30;
export const MAX_COMPRESSED_BYTES = 4_000_000;

export function bytesToBase64(bytes) {
  let binary = "";
  for (let start = 0; start < bytes.length; start += 8192) {
    binary += String.fromCharCode(...bytes.subarray(start, start + 8192));
  }
  return btoa(binary);
}

// Mono PCM16 WAV is accepted by CLIProxyAPI's input_audio translators.
export function encodeWav(samples, sampleRate = 16000) {
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  const label = (offset, value) => [...value].forEach((char, i) => view.setUint8(offset + i, char.charCodeAt(0)));
  label(0, "RIFF"); view.setUint32(4, buffer.byteLength - 8, true);
  label(8, "WAVE"); label(12, "fmt "); view.setUint32(16, 16, true);
  view.setUint16(20, 1, true); view.setUint16(22, 1, true);
  view.setUint32(24, sampleRate, true); view.setUint32(28, sampleRate * 2, true);
  view.setUint16(32, 2, true); view.setUint16(34, 16, true);
  label(36, "data"); view.setUint32(40, samples.length * 2, true);
  for (let i = 0; i < samples.length; i++) {
    const value = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(44 + i * 2, value < 0 ? value * 32768 : value * 32767, true);
  }
  return new Uint8Array(buffer);
}

export async function prepareAudio(blob) {
  if (!blob?.size || blob.size > MAX_COMPRESSED_BYTES) throw new Error("The recording is empty or too large. Record a short sentence again.");
  const decoder = new AudioContext();
  try {
    const decoded = await decoder.decodeAudioData(await blob.arrayBuffer());
    if (!Number.isFinite(decoded.duration) || decoded.duration <= 0 || decoded.duration > MAX_RECORDING_SECONDS + 1) {
      throw new Error("Record a clip of at most 30 seconds.");
    }
    const length = Math.min(Math.ceil(decoded.duration * 16000), MAX_RECORDING_SECONDS * 16000);
    const offline = new OfflineAudioContext(1, length, 16000);
    const source = offline.createBufferSource();
    source.buffer = decoded;
    source.connect(offline.destination);
    source.start();
    const rendered = await offline.startRendering();
    const samples = rendered.getChannelData(0);
    if (!samples.some(sample => Math.abs(sample) > 0.0001)) {
      throw new Error("The recording is silent. Check that the player is audible and record again.");
    }
    const bytes = encodeWav(samples);
    return { data: bytesToBase64(bytes), format: "wav", blob: new Blob([bytes], { type: "audio/wav" }) };
  } finally { await decoder.close(); }
}

export function recordStream(stream) {
  const tracks = stream.getAudioTracks();
  if (!tracks.length) {
    stream.getTracks().forEach(track => track.stop());
    throw new Error("No audio was shared. Choose a browser tab and enable Share tab audio.");
  }
  let recorder;
  try { recorder = new MediaRecorder(new MediaStream(tracks)); }
  catch (error) { stream.getTracks().forEach(track => track.stop()); throw error; }
  const chunks = [];
  let bytes = 0;
  let failure;
  let timer;
  let started = false;
  let canceled = false;
  const finished = new Promise((resolve, reject) => {
    const cleanup = () => {
      clearTimeout(timer);
      stream.getTracks().forEach(track => track.stop());
    };
    recorder.ondataavailable = event => {
      if (!event.data.size || failure) return;
      bytes += event.data.size;
      if (bytes > MAX_COMPRESSED_BYTES) { failure = new Error("The recording is too large."); stop(); }
      else chunks.push(event.data);
    };
    recorder.onerror = event => { failure = event.error || new Error("Recording failed."); stop(); };
    recorder.onstop = () => {
      cleanup();
      if (canceled) reject(new DOMException("Recording canceled.", "AbortError"));
      else if (failure) reject(failure);
      else resolve(new Blob(chunks, { type: recorder.mimeType }));
    };
    try { recorder.start(250); started = true; }
    catch (error) { cleanup(); reject(error); }
  });
  function stop() { if (recorder.state !== "inactive") recorder.stop(); }
  tracks.forEach(track => track.addEventListener("ended", stop, { once: true }));
  if (started) timer = setTimeout(stop, MAX_RECORDING_SECONDS * 1000);
  return { finished, stop, cancel() { canceled = true; stop(); } };
}

export function recordPlayer(tabId, frameId) {
  const port = browser.tabs.connect(tabId, { name: "audio-recording", frameId });
  let settled = false;
  let rejectFinished;
  let timer;
  const cleanup = () => { clearTimeout(timer); port.disconnect(); };
  const finished = new Promise((resolve, reject) => {
    rejectFinished = reject;
    port.onMessage.addListener(message => {
      if (settled) return;
      if (message?.type === "AUDIO_ERROR") {
        settled = true; reject(new Error(message.error || "Player recording failed.")); cleanup();
      } else if (message?.type === "AUDIO_CLIP") {
        settled = true;
        try {
          if (typeof message.data !== "string" || message.data.length > MAX_COMPRESSED_BYTES * 1.4) throw new Error("Invalid player recording.");
          const bytes = Uint8Array.from(atob(message.data), char => char.charCodeAt(0));
          resolve(new Blob([bytes], { type: message.mimeType }));
        } catch (error) { reject(error); }
        cleanup();
      }
    });
    port.onDisconnect.addListener(() => {
      clearTimeout(timer);
      if (!settled) { settled = true; reject(new Error("The player disconnected. Keep the page open and record again.")); }
    });
  });
  timer = setTimeout(() => {
    if (settled) return;
    settled = true;
    rejectFinished(new Error("The player did not finish recording. Record again."));
    cleanup();
  }, (MAX_RECORDING_SECONDS + 5) * 1000);
  port.postMessage({ type: "AUDIO_START" });
  return {
    finished,
    stop() { if (!settled) port.postMessage({ type: "AUDIO_STOP" }); },
    cancel() {
      if (settled) return;
      settled = true;
      rejectFinished(new DOMException("Recording canceled.", "AbortError"));
      cleanup();
    },
  };
}
