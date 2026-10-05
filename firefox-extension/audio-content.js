// Firefox captures the playing media element. Credentials and AI calls stay in the extension.
(() => {
  if (browser.isChrome) return;
  function players() {
    return [...document.querySelectorAll('video, audio')]
      .filter(player => !player.paused && !player.ended && player.readyState >= 2 &&
        (typeof player.captureStream === 'function' || typeof player.mozCaptureStream === 'function'))
      .map(player => ({ player, score: (player.muted ? 0 : 1000000) + player.clientWidth * player.clientHeight }))
      .sort((a, b) => b.score - a.score);
  }
  browser.runtime.onMessage.addListener((message, sender) => {
    if (message?.type !== 'AUDIO_PROBE' || sender.id !== browser.runtime.id) return undefined;
    const candidate = players()[0];
    return Promise.resolve({ available: Boolean(candidate), score: candidate?.score || 0 });
  });
  browser.runtime.onConnect.addListener(port => {
    if (port.name !== 'audio-recording' || port.sender?.id !== browser.runtime.id ||
        port.sender?.url?.split(/[?#]/u)[0] !== browser.runtime.getURL('sidebar.html')) return;
    let recorder, stream, timer, chunks = [], bytes = 0, closed = false, failure = '';
    const send = message => { if (!closed) { try { port.postMessage(message); } catch { closed = true; } } };
    const cleanup = () => { clearTimeout(timer); stream?.getTracks().forEach(track => track.stop()); };
    const stop = () => { if (recorder && recorder.state !== 'inactive') recorder.stop(); };
    port.onDisconnect.addListener(() => { closed = true; stop(); cleanup(); chunks = []; });
    port.onMessage.addListener(message => {
      if (message?.type === 'AUDIO_STOP') { stop(); return; }
      if (message?.type !== 'AUDIO_START' || recorder || closed) return;
      try {
        const player = players()[0]?.player;
        if (!player) throw new Error('Replay the video or audio before recording.');
        stream = (player.captureStream || player.mozCaptureStream).call(player);
        const tracks = stream.getAudioTracks();
        if (!tracks.length) throw new Error('This player has no capturable audio. Protected media cannot be recorded.');
        recorder = new MediaRecorder(new MediaStream(tracks));
        recorder.ondataavailable = event => {
          if (closed || failure || !event.data.size) return;
          bytes += event.data.size;
          if (bytes > 4_000_000) { failure = 'The recording is too large.'; stop(); }
          else chunks.push(event.data);
        };
        recorder.onerror = () => { failure = 'Player recording failed.'; stop(); };
        recorder.onstop = async () => {
          cleanup();
          if (closed) return;
          if (failure) { send({ type: 'AUDIO_ERROR', error: failure }); return; }
          try {
            const data = new Uint8Array(await new Blob(chunks, { type: recorder.mimeType }).arrayBuffer());
            chunks = [];
            let binary = '';
            for (let start = 0; start < data.length; start += 8192) binary += String.fromCharCode(...data.subarray(start, start + 8192));
            send({ type: 'AUDIO_CLIP', data: btoa(binary), mimeType: recorder.mimeType });
          } catch { send({ type: 'AUDIO_ERROR', error: 'Could not read the recording.' }); }
        };
        tracks.forEach(track => track.addEventListener('ended', stop, { once: true }));
        recorder.start(250);
        timer = setTimeout(stop, 30000);
      } catch (error) { cleanup(); send({ type: 'AUDIO_ERROR', error: error.message || 'Could not capture this player.' }); }
    });
  });
})();
