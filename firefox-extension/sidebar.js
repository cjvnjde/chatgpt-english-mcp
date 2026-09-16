import { renderMarkdown } from "./render.js";

const ui = Object.fromEntries([...document.querySelectorAll("[id]")].map(node => [node.id, node]));
let windowId;
let sidebarPort;
let state;
let earlyUpdates = [];
let localError = "";
let sending = false;
let saving = false;
let saveDialogError = "";
let stopping = false;
let clearing = false;
let imageUrls = [];
let lookupKey = "";
let lastAnnouncement = "";
const messageNodes = new Map();

function text(node, value) {
  const next = String(value || "");
  if (node.textContent !== next) node.textContent = next;
}

const browserThemeColorKeys = {
  "--browser-sidebar-background": ["sidebar", "toolbar", "frame"],
  "--browser-sidebar-text": ["sidebar_text", "toolbar_text", "tab_text"],
  "--browser-sidebar-border": ["sidebar_border", "toolbar_bottom_separator"],
  "--browser-sidebar-accent": ["sidebar_highlight", "button_background_hover"],
  "--browser-sidebar-accent-active": ["button_background_active", "sidebar_highlight"],
  "--browser-sidebar-accent-text": ["sidebar_highlight_text", "sidebar_text", "toolbar_text"],
  "--browser-sidebar-field": ["toolbar_field", "sidebar", "toolbar"],
  "--browser-sidebar-field-text": ["toolbar_field_text", "sidebar_text", "toolbar_text"],
};

function themeColor(value) {
  if (typeof value === "string") return value;
  if (!Array.isArray(value) || value.length < 3) return "";
  const components = value.slice(0, 3).map(Number);
  return components.every(Number.isFinite) ? `rgb(${components.join(" ")})` : "";
}

function applyBrowserTheme(theme = {}) {
  const root = document.documentElement;
  const colors = theme.colors || {};
  for (const [variable, keys] of Object.entries(browserThemeColorKeys)) {
    root.style.removeProperty(variable);
    for (const key of keys) {
      const color = themeColor(colors[key]);
      if (!color) continue;
      root.style.setProperty(variable, color);
      break;
    }
  }
  const scheme = theme.properties?.color_scheme;
  root.style.colorScheme = scheme === "light" || scheme === "dark" ? scheme : "";
}

async function refreshBrowserTheme() {
  if (!browser.theme?.getCurrent) {
    applyBrowserTheme();
    return;
  }
  try {
    applyBrowserTheme(await browser.theme.getCurrent(windowId));
  } catch {
    applyBrowserTheme();
  }
}

function safeURL(value) {
  try {
    const url = new URL(value);
    return ["https:", "http:"].includes(url.protocol) && !url.username && !url.password ? url : null;
  } catch { return null; }
}
function lines(value) {
  return String(value || "").split(/\r?\n/u).map(item => item.trim()).filter(Boolean);
}

function tags(value) {
  return String(value || "").split(",").map(item => item.trim()).filter(Boolean);
}

function updateSourceLink() {
  const source = safeURL(ui["save-source-url"].value.trim());
  ui["save-source-link"].hidden = !source;
  if (source) ui["save-source-link"].href = source.href;
  else ui["save-source-link"].removeAttribute("href");
}

function openSaveDialog() {
  const draft = state?.saveDraft;
  if (!draft) return;
  text(ui["save-term"], draft.term);
  ui["save-context"].value = draft.context || "";
  ui["save-tags"].value = (draft.tags || []).join(", ");
  ui["save-notes"].value = (draft.notes || []).join("\n");
  ui["save-examples"].value = (draft.examples || []).join("\n");
  ui["save-interest"].value = draft.personalInterest || "";
  ui["save-description"].value = draft.description || "";
  ui["save-source-title"].value = draft.sourceTitle || "";
  ui["save-source-url"].value = draft.sourceUrl || "";
  ui["save-details"].open = Boolean(draft.description || draft.sourceTitle || draft.sourceUrl);
  saveDialogError = "";
  updateSourceLink();
  if (!ui["save-dialog"].open) ui["save-dialog"].showModal();
  ui["save-context"].focus();
  renderControls();
}

function editedSaveDraft() {
  return {
    context: ui["save-context"].value,
    tags: tags(ui["save-tags"].value),
    notes: lines(ui["save-notes"].value),
    examples: lines(ui["save-examples"].value),
    personalInterest: ui["save-interest"].value,
    description: ui["save-description"].value,
    sourceTitle: ui["save-source-title"].value,
    sourceUrl: ui["save-source-url"].value,
  };
}

async function cancelSaveDialog() {
  if (ui["save-dialog"].open) ui["save-dialog"].close();
  saveDialogError = "";
  if (!state || !["ready", "error"].includes(state.saveStatus)) return;
  try { await command("SAVE_CANCEL", { conversationId: state.id }); }
  catch (error) {
    localError = `Could not close save details: ${error.message || error}`;
    renderControls();
  }
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
  const isPreparing = state.saveStatus === "preparing";
  const isSaving = state.saveStatus === "saving";
  const isBusy = saving || isPreparing || isSaving;
  const isSaved = state.saveStatus === "saved";
  const hasAnswer = state.messages.some(message => message.role === "assistant" && message.content.trim());
  ui.save.disabled = clearing || isBusy || isSaved || ui["save-dialog"].open || !state.selection || !state.settings.mcpConfigured || !state.settings.configured || state.status === "loading" || state.lookupStatus === "loading" || sending || !hasAnswer;
  const label = isPreparing ? "Preparing save details" : isSaving ? "Saving to dictionary" : isSaved ? "Saved to dictionary" : state.saveStatus === "ready" || state.saveDraft ? "Review save details" : state.saveStatus === "error" ? "Retry preparing save details" : "Save to dictionary";
  ui.save.setAttribute("aria-label", label);
  ui.save.setAttribute("title", label);
  ui.save.setAttribute("aria-busy", String(isBusy));
  ui["save-icon"].toggleAttribute("hidden", isSaved);
  ui["saved-icon"].toggleAttribute("hidden", !isSaved);
  text(ui["save-error"], state.saveError);
  ui["save-error"].hidden = !state.saveError;
  text(ui["save-notice"], state.saveNotice);
  ui["save-notice"].hidden = !state.saveNotice;
  const dialogError = saveDialogError || (ui["save-dialog"].open ? state.saveError : "");
  text(ui["save-dialog-error"], dialogError);
  ui["save-dialog-error"].hidden = !dialogError;
  ui["save-confirm"].disabled = isBusy || isSaved;
}

function renderControls() {
  if (!state) return;
  const loading = state.status === "loading";
  const lastMessage = state.messages.at(-1);
  const phase = lastMessage?.role === "assistant" && lastMessage.content.trim() ? "Answering…" : "Thinking…";
  ui["response-status"].hidden = !loading;
  text(ui["response-phase"], loading ? phase : "");
  ui.clear.hidden = !state.selection;
  ui.clear.disabled = clearing;
  ui.send.disabled = clearing || (loading ? stopping : !state.settings.configured || sending || !ui["message-input"].value.trim());
  ui.send.setAttribute("aria-label", loading ? stopping ? "Stopping response" : "Stop response" : "Send message");
  ui["send-icon"].toggleAttribute("hidden", loading);
  ui["stop-icon"].toggleAttribute("hidden", !loading);
  ui.messages.setAttribute("aria-busy", String(loading));
  ui["message-input"].setAttribute("aria-label", state.selection ? "Follow-up message" : "Word or phrase");
  const error = localError || state.error;
  text(ui["conversation-error"], error);
  ui["conversation-error"].hidden = !error;
  renderSave();
  announce(error || (loading ? phase : state.saveStatus === "error" ? state.saveError : state.saveStatus === "saved" ? state.saveNotice || "Saved to dictionary." : state.status === "ready" ? "Explanation ready." : ""));
}

function applyState(next) {
  if (!next || typeof next.revision !== "number" || (state && next.revision <= state.revision)) return;
  const follow = nearBottom();
  const changedConversation = state?.id !== next.id;
  state = next;
  if (changedConversation) {
    localError = "";
    saveDialogError = "";
    if (ui["save-dialog"].open) ui["save-dialog"].close();
    ui["context-disclosure"].open = false;
    lookupKey = "";
  }
  if (state.saveStatus === "saved" && ui["save-dialog"].open) ui["save-dialog"].close();
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
  if (!state || !message || clearing || sending || state.status === "loading" || !state.settings.configured) return;
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
  if (!state || clearing || stopping || state.status !== "loading") return;
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
ui.clear.addEventListener("click", async () => {
  if (!state?.selection || clearing) return;
  const conversationId = state.id;
  const draft = ui["message-input"].value;
  clearing = true; localError = ""; renderControls();
  try {
    const result = await command("SELECTION_CLEAR", { conversationId });
    if (state.id === result.state.id) {
      if (ui["message-input"].value === draft) {
        ui["message-input"].value = ""; resizeInput();
      }
      ui["message-input"].focus();
    }
  } catch (error) { if (state.id === conversationId) localError = error.message || String(error); }
  finally { clearing = false; renderControls(); }
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
  if (state.saveDraft) {
    openSaveDialog();
    return;
  }
  const conversationId = state.id;
  saving = true; localError = ""; renderControls();
  try {
    await command("SAVE_PREPARE", { conversationId });
    if (state.id === conversationId && state.saveDraft) openSaveDialog();
  } catch (error) {
    if (state.id === conversationId) localError = `Could not prepare save details: ${error.message || error}`;
  } finally {
    saving = false;
    renderControls();
  }
});
ui["save-form"].addEventListener("submit", async event => {
  event.preventDefault();
  if (!state?.saveDraft || saving) return;
  const conversationId = state.id;
  saving = true; saveDialogError = ""; localError = ""; renderControls();
  try {
    await command("DICTIONARY_SAVE", { conversationId, draft: editedSaveDraft() });
    if (state.id === conversationId && state.saveStatus !== "saved") {
      saveDialogError = state.saveError || "The vocabulary item could not be saved.";
    }
  } catch (error) {
    if (state.id === conversationId) saveDialogError = `Dictionary save failed: ${error.message || error}`;
  } finally {
    saving = false;
    renderControls();
  }
});
ui["save-cancel"].addEventListener("click", () => { void cancelSaveDialog(); });
ui["save-close"].addEventListener("click", () => { void cancelSaveDialog(); });
ui["save-dialog"].addEventListener("cancel", event => {
  event.preventDefault();
  void cancelSaveDialog();
});
ui["save-source-url"].addEventListener("input", updateSourceLink);

browser.runtime.onMessage.addListener(message => {
  if (message?.type !== "STATE_UPDATED") return;
  if (windowId === undefined) { earlyUpdates.push(message); return; }
  if (message.windowId === windowId) applyState(message.state);
});
if (browser.theme?.onUpdated) {
  browser.theme.onUpdated.addListener(({ theme, windowId: themedWindowId }) => {
    if (themedWindowId === undefined || themedWindowId === windowId)
      applyBrowserTheme(theme);
  });
}
window.addEventListener("unload", () => { sidebarPort?.disconnect(); }, { once: true });

async function start() {
  try {
    windowId = (await browser.windows.getCurrent()).id;
    await refreshBrowserTheme();
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
