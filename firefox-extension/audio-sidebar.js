import { loadSettings } from "./settings.js";
import { transcribeAudio } from "./api.js";
import { MAX_RECORDING_SECONDS, prepareAudio, recordPlayer, recordStream } from "./audio.js";

export function setupAudio({ ui, command, getState }) {
  let phase = "idle";
  let recording;
  let clip;
  let controller;
  let previewURL;
  let generation = 0;
  let timer;
  let errorText = "";
  let statusText = "";
  let explaining = false;
  const busy = () => ["starting", "recording", "preparing", "sending"].includes(phase);
  ui["audio-help"].textContent = browser.isChrome
    ? "Choose a browser tab and enable Share tab audio. Record up to 30 seconds, then preview and send to AI."
    : "Play a page video or audio player. Record up to 30 seconds, then preview and send to AI. Some protected players cannot be captured.";

  function update() {
    const state = getState();
    ui["record-audio"].disabled = !state?.settings.configured;
    ui["audio-start"].disabled = busy() || !state?.settings.configured;
    ui["audio-start"].textContent = clip || ui["audio-transcript"].value ? "Record again" : "Record";
    ui["audio-stop"].hidden = phase !== "recording";
    ui["audio-send"].hidden = !clip || ["starting", "recording", "preparing"].includes(phase);
    ui["audio-send"].disabled = phase === "sending" || !state?.settings.configured;
    ui["audio-cancel"].hidden = phase !== "sending";
    ui["audio-explain"].disabled = explaining || busy() || !state?.settings.configured || state.status === "loading" || state.saveStatus === "saving" || !ui["audio-term"].value.trim();
    ui["audio-status"].textContent = statusText;
    ui["audio-status"].dataset.recording = String(phase === "recording");
    ui["audio-error"].textContent = errorText;
    ui["audio-error"].hidden = !errorText;
  }

  function releasePreview() {
    ui["audio-preview"].pause();
    ui["audio-preview"].removeAttribute("src");
    ui["audio-preview"].load();
    ui["audio-preview"].hidden = true;
    if (previewURL) URL.revokeObjectURL(previewURL);
    previewURL = undefined;
    clip = undefined;
  }

  function cancel() {
    generation++;
    controller?.abort(); controller = undefined;
    recording?.cancel(); recording = undefined;
    clearInterval(timer);
    phase = "idle";
  }

  async function startRecording() {
    if (busy() || !getState()?.settings.configured) return;
    const current = ++generation;
    phase = "starting"; errorText = ""; statusText = "Starting recording…";
    releasePreview();
    ui["audio-result"].hidden = true;
    ui["audio-transcript"].value = "";
    ui["audio-term"].value = "";
    update();
    try {
      if (browser.isChrome) {
        // Keep getDisplayMedia in the click handler's activation, before any await.
        const stream = await navigator.mediaDevices.getDisplayMedia({
          video: { displaySurface: "browser" }, audio: { suppressLocalAudioPlayback: false },
          preferCurrentTab: false, selfBrowserSurface: "exclude",
          systemAudio: "exclude", surfaceSwitching: "exclude",
        });
        if (current !== generation) { stream.getTracks().forEach(track => track.stop()); return; }
        recording = recordStream(stream);
      } else {
        const source = await command("AUDIO_SOURCE");
        if (current !== generation) return;
        recording = recordPlayer(source.tabId, source.frameId);
      }
      phase = "recording";
      const started = Date.now();
      const tick = () => {
        statusText = `Recording · ${Math.min(MAX_RECORDING_SECONDS, Math.floor((Date.now() - started) / 1000))} / ${MAX_RECORDING_SECONDS}s`;
        update();
      };
      tick(); timer = setInterval(tick, 1000);
      const blob = await recording.finished;
      if (current !== generation) return;
      clearInterval(timer); recording = undefined;
      phase = "preparing"; statusText = "Preparing recording…"; update();
      const prepared = await prepareAudio(blob);
      if (current !== generation) return;
      clip = prepared;
      previewURL = URL.createObjectURL(clip.blob);
      ui["audio-preview"].src = previewURL;
      ui["audio-preview"].hidden = false;
      phase = "ready"; statusText = "Recording ready. Preview it, then send to AI.";
    } catch (error) {
      if (current !== generation) return;
      phase = "idle"; statusText = "";
      if (error.name !== "AbortError") errorText = error.message || "Could not record audio.";
    } finally {
      if (current === generation) { clearInterval(timer); recording = undefined; update(); }
    }
  }

  async function send() {
    if (!clip || busy()) return;
    const current = ++generation;
    controller = new AbortController();
    const signal = controller.signal;
    ui["audio-preview"].pause();
    phase = "sending"; errorText = ""; statusText = "Transcribing audio…"; update();
    try {
      const settings = await loadSettings();
      signal.throwIfAborted();
      const transcript = await transcribeAudio(settings, clip, { signal });
      if (current !== generation) return;
      ui["audio-transcript"].value = transcript;
      ui["audio-result"].hidden = false;
      statusText = "Transcript ready. Select the word or phrase you want to understand.";
      phase = "ready";
    } catch (error) {
      if (current !== generation) return;
      phase = "ready"; statusText = "";
      if (error.name !== "AbortError") errorText = error.message || "Audio analysis failed.";
    } finally {
      if (current === generation) { controller = undefined; update(); }
    }
  }

  ui["record-audio"].addEventListener("click", () => {
    ui["audio-panel"].hidden = false;
    ui.conversation.scrollTop = 0;
    // Opening the panel and starting Chrome capture happen in the same gesture.
    if (!busy() && !clip && !ui["audio-transcript"].value) void startRecording();
  });
  ui["audio-start"].addEventListener("click", () => { void startRecording(); });
  ui["audio-stop"].addEventListener("click", () => {
    clearInterval(timer);
    recording?.stop(); phase = "preparing"; statusText = "Finishing recording…"; update();
  });
  ui["audio-send"].addEventListener("click", () => { void send(); });
  ui["audio-cancel"].addEventListener("click", () => {
    cancel(); statusText = "Request canceled. You can send the recording again."; update();
  });
  ui["audio-close"].addEventListener("click", () => {
    cancel(); releasePreview();
    ui["audio-transcript"].value = ""; ui["audio-term"].value = "";
    ui["audio-result"].hidden = true; ui["audio-panel"].hidden = true;
    statusText = ""; errorText = ""; update();
  });
  ui["audio-transcript"].addEventListener("select", () => {
    const field = ui["audio-transcript"];
    const term = field.value.slice(field.selectionStart, field.selectionEnd).trim();
    if (term && Array.from(term).length <= 200) ui["audio-term"].value = term;
    update();
  });
  ui["audio-term"].addEventListener("input", update);
  ui["audio-explain-form"].addEventListener("submit", async event => {
    event.preventDefault();
    if (ui["audio-explain"].disabled) return;
    explaining = true; errorText = ""; update();
    try {
      await command("AUDIO_EXPLAIN", {
        conversationId: getState().id, term: ui["audio-term"].value,
        transcript: ui["audio-transcript"].value,
      });
    } catch (error) { errorText = error.message || "Could not explain this word."; }
    finally { explaining = false; update(); }
  });
  function dispose() {
    cancel(); releasePreview();
    statusText = ui["audio-transcript"].value ? "Transcript ready. Select a word or phrase to explain." : "";
    errorText = ""; update();
  }
  window.addEventListener("pagehide", dispose);
  // Chromium can retain a hidden side panel document after the user closes it.
  const panelEvents = browser.chromePanelEvents;
  panelEvents?.onClosed.addListener(({ windowId }) => {
    void browser.windows.getCurrent().then(current => { if (current.id === windowId) dispose(); }).catch(() => {});
  });
  return { update };
}
