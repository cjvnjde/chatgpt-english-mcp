import { renderMarkdown } from "./render.js";

const ui = Object.fromEntries([...document.querySelectorAll("[id]")].map(node => [node.id, node]));
let windowId;
let sidebarPort;
let state;
let earlyUpdates = [];
let localError = "";
let sending = false;
let saving = false;
let stopping = false;
let imageUrls = [];
let lookupKey = "";
let lastAnnouncement = "";
const messageNodes = new Map();

function text(node, value) {
  const next = String(value || "");
  if (node.textContent !== next) node.textContent = next;
}

function safeURL(value) {
  try {
    const url = new URL(value);
    return ["https:", "http:"].includes(url.protocol) && !url.username && !url.password ? url : null;
  } catch { return null; }
}

function announce(message) {
  if (message === lastAnnouncement) return;
  lastAnnouncement = message;
  text(ui.announcer, message);
}

function nearBottom() {
  return ui.conversation.scrollHeight - ui.conversation.scrollTop - ui.conversation.clientHeight < 72;
}

function toBottom() {
  ui.conversation.scrollTop = ui.conversation.scrollHeight;
}

function renderLookup() {
  const urls = new Set();
  const addImages = images => {
    for (const image of images || []) {
      for (const value of [image.imageUrl, image.thumbnailUrl]) {
        if (safeURL(value)?.protocol === "https:") urls.add(value);
      }
    }
  };
  addImages(state.lookup?.images);
  for (const entry of state.lookup?.entries || []) {
    for (const sense of entry.definitions || []) addImages(sense.images);
  }
  imageUrls = [...urls];
}

function renderSelection() {
  const selection = state.selection;
  ui["context-disclosure"].hidden = !selection;
  if (!selection) return;
  text(ui.term, selection.term);
  const source = state.contextMode !== "none" ? safeURL(selection.url) : null;
  ui["page-source"].hidden = !source;
  if (source) {
    ui["page-source"].href = source.href;
    text(ui["page-source"], selection.title || source.hostname);
  } else ui["page-source"].removeAttribute("href");
  const context = state.contextMode !== "none" ? selection.context || "" : "";
  text(ui["context-text"], context);
  ui["context-text"].hidden = !context;
  const hasContext = Boolean(source || context);
  ui["context-chevron"].toggleAttribute("hidden", !hasContext);
  ui["selection-summary"].setAttribute("aria-disabled", String(!hasContext));
  ui["selection-summary"].setAttribute("aria-label", hasContext ? `${selection.term}: show context` : selection.term);
  if (!hasContext) ui["context-disclosure"].open = false;
}

function renderMessages(force = false) {
  const wanted = new Set();
  let previous = null;
  for (const message of state.messages) {
    if (message.role === "user" && message.initial) continue;
    wanted.add(message.id);
    let record = messageNodes.get(message.id);
    if (!record) {
      const article = document.createElement("article");
      const body = document.createElement("div"); body.className = "markdown";
      article.append(body);
      record = { article, body, content: null, role: null };
      messageNodes.set(message.id, record);
    }
    const expected = previous ? previous.nextSibling : ui.messages.firstChild;
    if (expected !== record.article) ui.messages.insertBefore(record.article, expected);
    previous = record.article;
    const changedRole = record.role !== message.role;
    if (changedRole) {
      record.article.className = `message ${message.role === "user" ? "user-message" : "assistant-message"}`;
      record.article.setAttribute("aria-label", message.role === "user" ? "Your message" : "Explanation");
      record.role = message.role;
    }
    if (force || changedRole || record.content !== message.content) {
      if (message.role === "user") text(record.body, message.content);
      else record.body.replaceChildren(renderMarkdown(message.content, { imageUrls }));
      record.content = message.content;
    }
    record.article.hidden = !message.content;
  }
  for (const [id, record] of messageNodes) {
    if (!wanted.has(id)) { record.article.remove(); messageNodes.delete(id); }
  }
}

function renderSave() {
  const isSaving = saving || state.saveStatus === "saving";
  const isSaved = state.saveStatus === "saved";
  ui.save.disabled = isSaving || isSaved || !state.selection || !state.settings.mcpConfigured || !state.settings.configured || state.status === "loading" || state.lookupStatus === "loading" || sending || !state.messages.some(message => message.role === "assistant" && message.content.trim());
  ui.save.setAttribute("aria-label", isSaving ? "Saving to dictionary" : isSaved ? "Saved to dictionary" : state.saveStatus === "error" ? "Retry saving to dictionary" : "Save to dictionary");
  ui.save.setAttribute("aria-busy", String(isSaving));
  ui["save-icon"].toggleAttribute("hidden", isSaved);
  ui["saved-icon"].toggleAttribute("hidden", !isSaved);
  text(ui["save-error"], state.saveError);
  ui["save-error"].hidden = !state.saveError;
}

function renderControls() {
  if (!state) return;
  const loading = state.status === "loading";
  const lastMessage = state.messages.at(-1);
  const phase = lastMessage?.role === "assistant" && lastMessage.content.trim() ? "Answering…" : "Thinking…";
  ui["response-status"].hidden = !loading;
  text(ui["response-phase"], loading ? phase : "");
  ui.send.disabled = loading ? stopping : !state.settings.configured || sending || !ui["message-input"].value.trim();
  ui.send.setAttribute("aria-label", loading ? stopping ? "Stopping response" : "Stop response" : "Send message");
  ui["send-icon"].toggleAttribute("hidden", loading);
  ui["stop-icon"].toggleAttribute("hidden", !loading);
  ui.messages.setAttribute("aria-busy", String(loading));
  ui["message-input"].setAttribute("aria-label", state.selection ? "Follow-up message" : "Word or phrase");
  const error = localError || state.error;
  text(ui["conversation-error"], error);
  ui["conversation-error"].hidden = !error;
  renderSave();
  announce(error || (loading ? phase : state.saveStatus === "error" ? state.saveError : state.saveStatus === "saved" ? "Saved to dictionary." : state.status === "ready" ? "Explanation ready." : ""));
}

function applyState(next) {
  if (!next || typeof next.revision !== "number" || (state && next.revision <= state.revision)) return;
  const follow = nearBottom();
  const changedConversation = state?.id !== next.id;
  state = next;
  if (changedConversation) {
    localError = "";
    ui["context-disclosure"].open = false;
    lookupKey = "";
  }
  const nextLookupKey = `${state.id}:${state.lookup?.lookupId || ""}:${state.lookupStatus}`;
  const lookupChanged = lookupKey !== nextLookupKey;
  if (lookupChanged) { renderLookup(); lookupKey = nextLookupKey; }
  renderSelection();
  renderMessages(lookupChanged);
  renderControls();
  if (follow || changedConversation) toBottom();
}

async function command(type, fields = {}) {
  const result = await browser.runtime.sendMessage({ type, windowId, ...fields });
  if (!result?.ok) throw new Error(result?.error || "Could not complete the request.");
  if (result.state) applyState(result.state);
  return result;
}

async function sendMessage(value) {
  const message = value.trim();
  if (!state || !message || sending || state.status === "loading" || !state.settings.configured) return;
  const draft = ui["message-input"].value;
  const conversationId = state.id;
  sending = true; localError = ""; renderControls();
  try {
    await command("CHAT_SEND", { conversationId, text: message });
    if (ui["message-input"].value === draft && draft.trim() === message) {
      ui["message-input"].value = ""; resizeInput();
    }
    toBottom();
  } catch (error) { if (state.id === conversationId) localError = error.message || String(error); }
  finally { sending = false; renderControls(); }
}

async function stopResponse() {
  if (!state || stopping || state.status !== "loading") return;
  const conversationId = state.id;
  stopping = true; localError = ""; renderControls();
  try { await command("CHAT_STOP", { conversationId }); }
  catch (error) { if (state.id === conversationId) localError = error.message || String(error); }
  finally { stopping = false; renderControls(); }
}

function resizeInput() {
  ui["message-input"].style.height = "auto";
  ui["message-input"].style.height = `${Math.min(132, ui["message-input"].scrollHeight)}px`;
}

ui.settings.addEventListener("click", async () => {
  try { await browser.runtime.openOptionsPage(); }
  catch (error) {
    localError = `Could not open settings: ${error.message || error}`;
    if (state) renderControls();
    else { text(ui["conversation-error"], localError); ui["conversation-error"].hidden = false; }
  }
});
ui["selection-summary"].addEventListener("click", event => {
  if (ui["selection-summary"].getAttribute("aria-disabled") === "true") event.preventDefault();
});
ui.composer.addEventListener("submit", event => {
  event.preventDefault();
  if (state?.status === "loading") void stopResponse();
  else void sendMessage(ui["message-input"].value);
});
ui["message-input"].addEventListener("input", () => { resizeInput(); renderControls(); });
ui["message-input"].addEventListener("keydown", event => {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    void sendMessage(ui["message-input"].value);
  }
});
ui.save.addEventListener("click", async () => {
  if (!state || ui.save.disabled || saving) return;
  const conversationId = state.id;
  saving = true; localError = ""; renderControls();
  try { await command("DICTIONARY_SAVE", { conversationId }); }
  catch (error) { if (state.id === conversationId) localError = `Dictionary save failed: ${error.message || error}`; }
  finally { saving = false; renderControls(); }
});

browser.runtime.onMessage.addListener(message => {
  if (message?.type !== "STATE_UPDATED") return;
  if (windowId === undefined) { earlyUpdates.push(message); return; }
  if (message.windowId === windowId) applyState(message.state);
});
window.addEventListener("unload", () => { sidebarPort?.disconnect(); }, { once: true });

async function start() {
  try {
    windowId = (await browser.windows.getCurrent()).id;
    sidebarPort = browser.runtime.connect({ name: `sidebar:${windowId}` });
    for (const message of earlyUpdates) if (message.windowId === windowId) applyState(message.state);
    earlyUpdates = [];
    await command("SIDEBAR_GET");
    resizeInput();
  } catch (error) {
    text(ui["conversation-error"], `Could not open English Dictionary: ${error.message || error}`);
    ui["conversation-error"].hidden = false;
  }
}

void start();
