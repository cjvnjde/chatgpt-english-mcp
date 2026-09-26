import { loadSettings } from './settings.js';
import { completeChat } from './api.js';
import { MCPClient } from './mcp.js';
import { HOST_SYSTEM_GUARD, SAVE_DRAFT_SYSTEM_PROMPT } from './prompt.js';

let settings = await loadSettings();
const windows = new Map();
const focusedFrames = new Map();
const privateSelectionMessage = 'Editable fields are excluded. Type a word in the sidebar instead.';
const extensionURL = browser.runtime.getURL('');
const MAX_TERM = 200;
const MAX_SAVE_CONTEXT = 400;
const MAX_SAVE_DESCRIPTION = 5000;

function configuration() {
  return { configured: Boolean(settings.aiUrl && settings.model), mcpConfigured: Boolean(settings.mcpUrl), model: settings.model };
}

function blankState(revision = 0) {
  return {
    revision, id: crypto.randomUUID(), selection: null, contextMode: settings.contextMode,
    messages: [], lookup: null, lookupStatus: 'idle', status: 'idle', error: '',
    saveStatus: 'idle', saveError: '', saveNotice: '', saveDraft: null, savedItemId: '',
    settings: configuration(),
  };
}

function windowRecord(windowId) {
  if (!Number.isInteger(windowId) || windowId < 0) throw new Error('No Firefox window is available.');
  if (!windows.has(windowId)) windows.set(windowId, { state: blankState(), controller: null, quickController: null, saveController: null, selectionRequest: 0, ports: new Set(), pendingDeep: false, needsDeepCapture: false, source: null, saveSense: null });
  return windows.get(windowId);
}

function publish(record) {
  record.state.revision++;
  browser.runtime.sendMessage({ type: 'STATE_UPDATED', windowId: record.windowId, state: record.state }).catch(() => {});
}

function recordFor(windowId) {
  const record = windowRecord(windowId);
  record.windowId = windowId;
  return record;
}

function normalizeTerm(text) {
  const term = String(text || '').replace(/\s+/gu, ' ').trim();
  if (!term || Array.from(term).length > MAX_TERM || term.includes('\0')) throw new Error('Select a word or phrase of 1–200 characters.');
  return term;
}

function truncateText(value, maximum) {
  return Array.from(String(value || '')).slice(0, maximum).join('');
}

function cleanDraftText(value, maximum) {
  return truncateText(String(value || '').replaceAll('\0', '').replace(/\s+/gu, ' ').trim(), maximum);
}

function cleanDraftList(value, maximumItems, maximumLength, transform = item => item) {
  if (!Array.isArray(value)) return [];
  const result = [];
  for (const item of value) {
    const cleaned = transform(cleanDraftText(item, maximumLength));
    if (cleaned && !result.includes(cleaned)) result.push(cleaned);
    if (result.length === maximumItems) break;
  }
  return result;
}

function conciseContext(value, fallback = '') {
  const raw = truncateText(String(value || fallback || '').replaceAll('\0', '').trim(), 1200);
  const phrases = raw.split(/(?<=[.!?])\s+|\s*[\n;|]\s*/u)
    .map(phrase => cleanDraftText(phrase, 180))
    .filter(Boolean)
    .slice(0, 4);
  return cleanDraftText(phrases.join('; '), MAX_SAVE_CONTEXT);
}

function encounterFallback(selection) {
  const excerpt = cleanDraftText(selection?.context, 1200);
  const term = cleanDraftText(selection?.term, MAX_TERM).toLocaleLowerCase();
  const pieces = excerpt.split(/(?<=[.!?])\s+/u).filter(Boolean);
  const matching = pieces.find(piece => piece.toLocaleLowerCase().includes(term)) || pieces[0] || '';
  const title = cleanDraftText(selection?.title, 160);
  return [title, matching].filter(Boolean).join('; ');
}

function cleanSourceURL(value) {
  if (!value) return '';
  try {
    const parsed = new URL(String(value));
    if (!['https:', 'http:'].includes(parsed.protocol) || parsed.username || parsed.password) return '';
    return truncateText(parsed.href, 2000);
  } catch {
    return '';
  }
}

function modelJSON(value, errorMessage) {
  try {
    return JSON.parse(String(value).trim().replace(/^```(?:json)?\s*/u, '').replace(/\s*```$/u, ''));
  } catch {
    throw new Error(errorMessage);
  }
}

function saveSenses(state) {
  return (state.lookup?.entries || []).flatMap(entry =>
    (entry.definitions || []).filter(item => typeof item.definition === 'string' && item.definition.trim())
      .map(item => ({ term: entry.headword || state.selection.term, definition: item.definition })));
}

function explanationFor(state) {
  return state.messages.find(item => item.role === 'assistant' && item.content.trim())?.content || '';
}

function mergeDraftLists(existing, additions, maximumItems, maximumLength, transform = item => item) {
  return cleanDraftList([...(existing || []), ...(additions || [])], maximumItems, maximumLength, transform);
}

function cleanSelection(selection, mode) {
  const term = normalizeTerm(selection.term);
  let url = '';
  if (mode !== 'none' && selection.url) {
    try {
      const parsed = new URL(selection.url);
      if (['https:', 'http:'].includes(parsed.protocol)) {
        parsed.username = ''; parsed.password = ''; parsed.search = ''; parsed.hash = '';
        url = parsed.href;
      }
    } catch { /* A source link is optional. */ }
  }
  return {
    term, context: mode === 'none' ? '' : String(selection.context || '').replaceAll('\0', '').slice(0, mode === 'page' ? 12000 : 2400),
    title: mode === 'none' ? '' : String(selection.title || '').replaceAll('\0', '').slice(0, 300), url,
  };
}

function dictionaryReference(lookup) {
  if (!lookup) return null;
  return {
    source: lookup.source,
    entries: (lookup.entries || []).slice(0, 12).map(entry => ({
      headword: entry.headword, partOfSpeech: entry.partOfSpeech, pronunciations: entry.pronunciations,
      definitions: (entry.definitions || []).slice(0, 12).map(definition => ({
        definition: definition.definition, examples: definition.examples?.slice(0, 3), labels: definition.labels,
        images: definition.images,
      })),
    })),
    images: lookup.images,
  };
}

function promptMessages(state, requestSettings) {
  const system = `${requestSettings.systemPrompt}

${HOST_SYSTEM_GUARD}
REFERENCE DATA: ${JSON.stringify({ selection: state.selection, dictionary: dictionaryReference(state.lookup) })}`;
  const history = state.messages.filter(message => message.content.trim()).map(({ role, content }) => ({ role, content }));
  const first = history.shift();
  const recent = [];
  let size = 0;
  for (let index = history.length - 1; index >= 0; index--) {
    if (size + history[index].content.length > 28000) break;
    recent.unshift(history[index]);
    size += history[index].content.length;
  }
  return [{ role: 'system', content: system }, ...(first ? [first] : []), ...recent];
}

async function respond(record, initial = false) {
  const state = record.state;
  const requestSettings = { ...settings };
  const source = record.source;
  const recapture = record.needsDeepCapture;
  const mode = source ? requestSettings.contextMode : 'none';
  const controller = new AbortController();
  record.controller = controller;
  state.selection = cleanSelection(recapture ? { term: state.selection.term } : state.selection, mode);
  state.contextMode = mode;
  state.status = 'loading'; state.error = '';
  publish(record);
  const current = () => record.state === state && record.controller === controller && !controller.signal.aborted;
  try {
    if (!requestSettings.aiUrl || !requestSettings.model) throw new Error('Choose a model in Settings.');
    if (recapture && source?.sourceId) {
      let captured;
      try {
        captured = await browser.tabs.sendMessage(source.tabId, {
          type: 'CAPTURE_SELECTION', useCachedSelection: true, contextMode: mode,
          sourceId: source.sourceId, expectedTerm: state.selection.term,
        }, { frameId: source.frameId });
      } catch { /* A navigated or unavailable source safely falls back to term-only. */ }
      if (!current()) return;
      if (captured?.sourceId === source.sourceId && String(captured.term || '').replace(/\s+/gu, ' ').trim() === state.selection.term) {
        state.selection = cleanSelection({ ...captured, term: state.selection.term }, mode);
      }
    }
    if (!current()) return;
    record.needsDeepCapture = false;
    if (initial && requestSettings.mcpUrl) {
      state.lookupStatus = 'loading'; publish(record);
      try {
        const client = new MCPClient(requestSettings);
        await client.connect({ signal: controller.signal });
        const lookup = await client.callTool('dictionary_lookup', { term: state.selection.term }, { signal: controller.signal });
        if (!current()) return;
        state.lookup = lookup; state.lookupStatus = 'ready';
      } catch (error) {
        if (!current()) return;
        state.lookupStatus = 'unavailable';
      }
      publish(record);
    }
    if (!current()) return;
    const messages = promptMessages(state, requestSettings);
    const answer = { id: crypto.randomUUID(), role: 'assistant', content: '' };
    state.messages.push(answer);
    publish(record);
    const text = await completeChat(requestSettings, messages, {
      signal: controller.signal,
      onDelta(content) {
        if (!current()) return;
        answer.content = content;
        publish(record);
      },
    });
    if (!current()) return;
    answer.content = text;
    state.status = 'ready';
  } catch (error) {
    if (!current()) return;
    state.status = 'error'; state.error = error.message || 'The explanation could not be completed.';
    state.messages = state.messages.filter(message => message.content.trim());
  } finally {
    if (record.state === state && record.controller === controller) {
      record.controller = null;
      if (state.lookupStatus === 'loading') state.lookupStatus = 'unavailable';
      publish(record);
    }
  }
}

function invalidateSave(record) {
  record.saveController?.abort();
  record.saveController = null;
  record.saveSense = null;
}

function beginSelection(record, selection, mode = settings.contextMode, source = null, needsDeepCapture = false) {
  const cleaned = cleanSelection(selection, mode);
  invalidateSave(record);
  record.controller?.abort();
  record.controller = null;
  record.quickController?.abort();
  record.quickController = null;
  record.source = source;
  record.saveSense = null;
  record.needsDeepCapture = needsDeepCapture;
  record.state = blankState(record.state.revision);
  const state = record.state;
  state.selection = cleaned; state.contextMode = mode;
  state.messages.push({ id: crypto.randomUUID(), role: 'user', initial: true, content: `What does '${cleaned.term}' mean?` });
  record.pendingDeep = true;
  publish(record);
}

function checkConversation(record, message) {
  if (message.conversationId !== record.state.id) throw new Error('The selected word has changed. Please try again.');
}
async function dismissQuick(record) {
  record.quickController?.abort();
  record.quickController = null;
  if (record.source) {
    await browser.tabs.sendMessage(record.source.tabId, { type: 'DISMISS_QUICK' }, { frameId: record.source.frameId }).catch(() => {});
  }
}

function startDeep(record) {
  void dismissQuick(record);
  if (!record.pendingDeep || !record.state.selection) return;
  record.pendingDeep = false;
  void respond(record, record.state.lookupStatus === 'idle');
}

async function quickExplanation(record, requestId) {
  const state = record.state;
  const source = record.source;
  const controller = new AbortController();
  record.quickController = controller;
  const requestSettings = { ...settings, model: settings.quickModel || settings.model, thinkingLevel: settings.quickThinkingLevel };
  state.selection = cleanSelection(state.selection, requestSettings.quickContextMode);
  state.contextMode = requestSettings.quickContextMode;
  publish(record);
  const current = () => record.state === state && record.quickController === controller && !controller.signal.aborted;
  let updates = Promise.resolve();
  try {
    const text = await completeChat(requestSettings, [
      { role: 'system', content: `${requestSettings.quickSystemPrompt}

${HOST_SYSTEM_GUARD}
REFERENCE DATA: ${JSON.stringify({ selection: state.selection })}` },
      { role: 'user', content: state.messages[0].content },
    ], {
      signal: controller.signal,
      onDelta(text) {
        updates = updates.then(async () => {
          if (!current()) return;
          const open = record.ports.size || await browser.sidebarAction.isOpen({ windowId: record.windowId });
          if (!current()) return;
          if (open) { startDeep(record); return; }
          await browser.tabs.sendMessage(source.tabId, { type: 'QUICK_UPDATED', requestId, text }, { frameId: source.frameId }).catch(() => {});
        }).catch(() => {});
      },
    });
    await updates;
    if (!current()) return { ok: true, mode: 'canceled' };
    const open = record.ports.size || await browser.sidebarAction.isOpen({ windowId: record.windowId });
    if (!current()) return { ok: true, mode: 'canceled' };
    if (open) {
      startDeep(record);
      return { ok: true, mode: 'sidebar' };
    }
    return { ok: true, mode: 'quick', text };
  } catch (error) {
    await updates;
    if (!current()) return { ok: true, mode: 'canceled' };
    throw error;
  } finally {
    if (record.quickController === controller) record.quickController = null;
  }
}

async function explainFromTab(tab, frameId, useCachedSelection = false, fallbackTerm, requestId) {
  if (!tab?.id) throw new Error('Select a word on a web page first.');
  frameId ??= focusedFrames.get(tab.id) ?? 0;
  const record = recordFor(tab.windowId);
  const request = ++record.selectionRequest;
  record.quickController?.abort(); record.quickController = null;
  record.controller?.abort(); record.controller = null;
  if (record.state.status === 'loading') {
    record.state.messages = record.state.messages.filter(item => item.content.trim());
    record.state.status = 'idle';
    if (record.state.lookupStatus === 'loading') record.state.lookupStatus = 'unavailable';
    publish(record);
  }
  record.pendingDeep = false;
  const initiallyOpen = record.ports.size || await browser.sidebarAction.isOpen({ windowId: tab.windowId });
  if (request !== record.selectionRequest) return { ok: true, mode: 'canceled' };
  const mode = initiallyOpen ? settings.contextMode : settings.quickContextMode;
  let captured;
  try {
    captured = await browser.tabs.sendMessage(tab.id, { type: 'CAPTURE_SELECTION', useCachedSelection, contextMode: mode }, { frameId });
  } catch { /* Firefox-protected pages do not permit content scripts. */ }
  if (request !== record.selectionRequest) return { ok: true, mode: 'canceled' };
  if (captured?.privacyDenied) throw new Error(privateSelectionMessage);
  if (captured && fallbackTerm && normalizeTerm(captured.term) !== normalizeTerm(fallbackTerm)) captured = null;
  if (!captured && fallbackTerm) captured = { term: fallbackTerm, context: '', title: '', url: '' };
  if (!captured) throw new Error('Select text on a regular web page, or type a word in the sidebar. Firefox blocks extensions on some pages, including its PDF viewer.');
  const source = { tabId: tab.id, frameId, sourceId: captured.sourceId };
  beginSelection(record, captured, mode, source, !initiallyOpen);
  const state = record.state;
  const open = record.ports.size || await browser.sidebarAction.isOpen({ windowId: tab.windowId });
  if (request !== record.selectionRequest || record.state !== state) return { ok: true, mode: 'canceled' };
  if (open) {
    startDeep(record);
    return { ok: true, mode: 'sidebar' };
  }
  // Do not reuse a Deep capture in Quick if its sidebar closed during capture.
  if (initiallyOpen) return { ok: true, mode: 'canceled' };
  return quickExplanation(record, requestId);
}

function editableSaveDraft(input, base) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('Save details are missing.');
  const description = cleanDraftText(input.description, MAX_SAVE_DESCRIPTION);
  const sourceTitle = cleanDraftText(input.sourceTitle, 200);
  const enteredURL = cleanDraftText(input.sourceUrl, 2000);
  const sourceUrl = cleanSourceURL(enteredURL);
  if (enteredURL && !sourceUrl) throw new Error('Source URL must be a complete HTTP or HTTPS URL.');
  if ((sourceTitle || sourceUrl) && !description) throw new Error('Add a description before attaching its source page.');
  const personalInterest = String(input.personalInterest || '');
  if (!['', 'low', 'normal', 'high'].includes(personalInterest)) throw new Error('Choose a valid personal interest.');
  return {
    term: base.term,
    description,
    context: conciseContext(input.context),
    notes: cleanDraftList(input.notes, 100, 1000),
    examples: cleanDraftList(input.examples, 100, 1000),
    tags: cleanDraftList(input.tags, 50, 50, tag => tag.toLocaleLowerCase()),
    sourceTitle,
    sourceUrl,
    personalInterest,
  };
}

async function prepareSaveDraft(record, message) {
  checkConversation(record, message);
  const state = record.state;
  if (!state.selection || state.status === 'loading') throw new Error('Wait for the explanation before saving.');
  if (state.saveStatus === 'preparing' || state.saveStatus === 'saving' || state.saveStatus === 'saved') return;
  if (!settings.mcpUrl) throw new Error('Connect English MCP in Settings before saving.');
  const explanation = explanationFor(state);
  if (!explanation) throw new Error('Get an explanation before adding this word.');
  const senses = saveSenses(state);
  const requestSettings = { ...settings };
  const controller = new AbortController();
  record.saveController = controller;
  const current = () => record.state === state && record.saveController === controller && !controller.signal.aborted;
  state.saveStatus = 'preparing'; state.saveError = ''; state.saveNotice = ''; publish(record);
  try {
    const decision = await completeChat(requestSettings, [
      { role: 'system', content: SAVE_DRAFT_SYSTEM_PROMPT },
      {
        role: 'user',
        content: JSON.stringify({
          selection: state.selection,
          explanation,
          conversation: state.messages.map(({ role, content }) => ({ role, content })),
          senses,
        }),
      },
    ], { signal: controller.signal });
    if (!current()) return;
    const generated = modelJSON(decision, 'Could not prepare editable save details. Try again.');
    const index = generated.index ?? null;
    if (index !== null && (!Number.isInteger(index) || !senses[index])) {
      throw new Error('The model returned an invalid dictionary meaning.');
    }
    const sense = index === null ? undefined : senses[index];
    record.saveSense = sense || null;
    let sourceTitle = cleanDraftText(state.selection.title, 200);
    const sourceUrl = cleanSourceURL(state.selection.url);
    if (!sourceTitle && sourceUrl) sourceTitle = new URL(sourceUrl).hostname;
    state.saveDraft = {
      term: sense?.term || state.selection.term,
      description: cleanDraftText(generated.description, 1500) || cleanDraftText(explanation, 1500),
      context: conciseContext(generated.context, encounterFallback(state.selection)),
      notes: cleanDraftList(generated.notes, 3, 1000),
      examples: cleanDraftList(generated.examples, 2, 1000),
      tags: cleanDraftList(generated.tags, 5, 50, tag => tag.toLocaleLowerCase()),
      sourceTitle,
      sourceUrl,
      personalInterest: '',
    };
    state.saveStatus = 'ready';
  } catch (error) {
    if (!current()) return;
    record.saveSense = null;
    state.saveDraft = null;
    state.saveStatus = 'error';
    state.saveError = error.message || 'Save details could not be prepared.';
  }
  if (current()) { record.saveController = null; publish(record); }
}

async function saveToDictionary(record, message) {
  checkConversation(record, message);
  const state = record.state;
  if (!state.selection || state.status === 'loading') throw new Error('Wait for the explanation before saving.');
  if (state.saveStatus === 'saving' || state.saveStatus === 'saved') return;
  if (!settings.mcpUrl) throw new Error('Connect English MCP in Settings before saving.');
  if (!state.saveDraft) throw new Error('Open Save and review the details first.');
  const draft = editableSaveDraft(message.draft || state.saveDraft, state.saveDraft);
  const sense = record.saveSense;
  const requestSettings = { ...settings };
  const controller = new AbortController();
  record.saveController = controller;
  const current = () => record.state === state && record.saveController === controller && !controller.signal.aborted;
  state.saveStatus = 'saving'; state.saveError = ''; state.saveNotice = ''; publish(record);
  try {
    const client = new MCPClient(requestSettings);
    await client.connect({ signal: controller.signal });
    if (!current()) return;
    const args = {
      term: draft.term,
      status: 'new',
      customDescription: draft.description,
      context: draft.context,
      notes: draft.notes,
      examples: draft.examples,
      tags: draft.tags,
    };
    if (sense) args.definition = sense.definition;
    if (draft.personalInterest) args.personalInterest = draft.personalInterest;
    if (draft.sourceTitle || draft.sourceUrl) {
      args.descriptionSource = { title: draft.sourceTitle, url: draft.sourceUrl };
    }
    // Once sent, a write cannot be undone. Do not cancel/replay the transport;
    // only suppress stale results and any subsequent update.
    let result = await client.callTool('vocabulary_save', args);
    if (!current()) return;
    if (!result.itemId) throw new Error('The MCP response did not confirm a saved vocabulary item.');
    if (!result.created) {
      const changes = {
        context: draft.context,
        tags: mergeDraftLists(result.tags, draft.tags, 50, 50, tag => tag.toLocaleLowerCase()),
        notes: mergeDraftLists(result.notes, draft.notes, 100, 1000),
        examples: mergeDraftLists(result.examples, draft.examples, 100, 1000),
        customDescription: draft.description,
        descriptionSource: draft.sourceTitle || draft.sourceUrl
          ? { title: draft.sourceTitle, url: draft.sourceUrl }
          : {},
      };
      if (draft.personalInterest) changes.personalInterest = draft.personalInterest;
      result = await client.callTool('vocabulary_update', { itemId: result.itemId, changes });
      if (!current()) return;
      if (!result.itemId) throw new Error('English MCP did not confirm the vocabulary update.');
    }
    state.saveStatus = 'saved';
    state.savedItemId = result.itemId;
    state.saveNotice = result.created
      ? 'Added to your vocabulary.'
      : 'Existing vocabulary updated with your details.';
  } catch (error) {
    if (!current()) return;
    state.saveStatus = 'error'; state.saveError = error.message || 'Dictionary saving failed.';
  }
  if (current()) { record.saveController = null; publish(record); }
}

function isPage(sender, name) {
  if (sender.id !== browser.runtime.id || !sender.url) return false;
  return sender.url.split(/[?#]/u)[0] === extensionURL + name;
}

async function handleMessage(message, sender) {
  if (!message || typeof message.type !== 'string') return undefined;
  if (message.type === 'STATE_UPDATED') return undefined;
  if (message.type === 'SELECTION_FRAME_FOCUSED') {
    if (sender.id !== browser.runtime.id || !sender.tab || !/^https?:/u.test(sender.url || '') || !Number.isInteger(sender.frameId)) return;
    focusedFrames.set(sender.tab.id, sender.frameId);
    return;
  }
  if (message.type === 'EXPLAIN_SELECTION') {
    if (sender.id !== browser.runtime.id || !sender.tab || !/^https?:/u.test(sender.url || '')) throw new Error('Unsupported selection source.');
    return explainFromTab(sender.tab, sender.frameId, true, undefined, message.requestId);
  }
  if (!isPage(sender, 'sidebar.html')) throw new Error('This action is only available in the English Dictionary sidebar.');
  const record = recordFor(message.windowId);
  switch (message.type) {
    case 'SIDEBAR_GET':
      startDeep(record);
      return { ok: true, state: record.state };
    case 'SELECTION_CLEAR':
      checkConversation(record, message);
      record.selectionRequest++;
      record.controller?.abort(); record.controller = null;
      void dismissQuick(record);
      record.source = null;
      invalidateSave(record);
      record.pendingDeep = false;
      record.needsDeepCapture = false;
      record.state = blankState(record.state.revision);
      publish(record); break;
    case 'CHAT_STOP':
      checkConversation(record, message);
      record.controller?.abort(); record.controller = null;
      record.state.messages = record.state.messages.filter(item => item.content.trim());
      record.state.status = record.state.selection ? 'ready' : 'idle';
      if (record.state.lookupStatus === 'loading') record.state.lookupStatus = 'unavailable';
      publish(record); break;
    case 'CHAT_SEND': {
      checkConversation(record, message);
      if (record.state.status === 'loading') throw new Error('Wait for the reply or press Stop.');
      const text = String(message.text || '').trim();
      if (!text || text.length > 6000 || text.includes('\0')) throw new Error('Enter a question of 1–6,000 characters.');
      if (record.state.saveStatus !== 'saved') {
        invalidateSave(record);
        record.state.saveDraft = null;
        record.state.saveStatus = 'idle';
        record.state.saveError = '';
      }
      if (!record.state.selection) {
        record.selectionRequest++;
        beginSelection(record, { term: text }, 'none');
        startDeep(record);
      }
      else {
        record.state.messages.push({ id: crypto.randomUUID(), role: 'user', content: text });
        void respond(record, record.state.lookupStatus === 'idle');
      }
      break;
    }
    case 'SAVE_PREPARE': await prepareSaveDraft(record, message); break;
    case 'SAVE_CANCEL':
      checkConversation(record, message);
      if (record.state.saveStatus !== 'saving' && record.state.saveStatus !== 'saved') {
        invalidateSave(record);
        record.state.saveDraft = null;
        record.state.saveStatus = 'idle';
        record.state.saveError = '';
        record.state.saveNotice = '';
        publish(record);
      }
      break;
    case 'DICTIONARY_SAVE': await saveToDictionary(record, message); break;
    default: throw new Error('Unknown English Dictionary action.');
  }
  return { ok: true, state: record.state };
}

browser.runtime.onMessage.addListener((message, sender) => {
  // Do not claim our own broadcast messages; their listeners need no response.
  if (message?.type === 'STATE_UPDATED') return undefined;
  return handleMessage(message, sender).catch(error => ({ ok: false, error: error.message }));
});

browser.menus.create({ id: 'english-dictionary-explain', title: 'Explain “%s” in sidebar', contexts: ['selection'] });
browser.menus.onClicked.addListener((info, tab) => {
  if (info.menuItemId !== 'english-dictionary-explain') return;
  if (info.editable) { reportActionError(tab?.windowId, new Error(privateSelectionMessage)); return; }
  browser.sidebarAction.open().then(() => explainFromTab(tab, info.frameId, false, info.selectionText))
    .catch(error => reportActionError(tab?.windowId, error));
});

browser.runtime.onConnect.addListener(port => {
  if (!isPage(port.sender, 'sidebar.html') || !/^sidebar:\d+$/u.test(port.name)) return;
  const record = recordFor(Number(port.name.slice('sidebar:'.length)));
  record.ports.add(port);
  startDeep(record);
  port.onDisconnect.addListener(() => {
    record.ports.delete(port);
    if (record.ports.size || !record.controller) return;
    record.controller.abort(); record.controller = null;
    record.pendingDeep = true;
    if (record.state.messages.at(-1)?.role === 'assistant') record.state.messages.pop();
    if (record.state.lookupStatus === 'loading') record.state.lookupStatus = 'idle';
    record.state.status = 'idle';
    publish(record);
  });
});
function reportActionError(windowId, error) {
  if (!Number.isInteger(windowId)) return;
  const record = recordFor(windowId);
  record.state.error = error.message; publish(record);
}

function openFromToolbar(explain = false) {
  const opening = explain ? browser.sidebarAction.toggle() : browser.sidebarAction.open();
  opening.then(async () => {
    const [tab] = await browser.tabs.query({ active: true, currentWindow: true });
    if (!tab) return;
    if (explain && !await browser.sidebarAction.isOpen({ windowId: tab.windowId })) return;
    const record = recordFor(tab.windowId);
    if (!explain && record.state.selection) { startDeep(record); return; }
    try { await explainFromTab(tab); } catch (error) {
      if (!recordFor(tab.windowId).state.selection) reportActionError(tab.windowId, error);
    }
  }).catch(async error => {
    const [tab] = await browser.tabs.query({ active: true, currentWindow: true });
    reportActionError(tab?.windowId, error);
  });
}
browser.browserAction.onClicked.addListener(() => openFromToolbar());
browser.commands.onCommand.addListener(command => { if (command === 'explain-selection') openFromToolbar(true); });
browser.tabs.onRemoved?.addListener(tabId => focusedFrames.delete(tabId));
browser.tabs.onUpdated?.addListener((tabId, change) => {
  if (change.status === 'loading') focusedFrames.delete(tabId);
});
browser.windows.onRemoved.addListener(windowId => {
  const record = windows.get(windowId);
  if (record) { record.selectionRequest++; invalidateSave(record); }
  record?.controller?.abort(); record?.quickController?.abort(); windows.delete(windowId);
});
browser.storage.onChanged.addListener(async (changes, area) => {
  if (area !== 'local' || !changes.settings) return;
  settings = await loadSettings();
  for (const record of windows.values()) { record.state.settings = configuration(); publish(record); }
});
