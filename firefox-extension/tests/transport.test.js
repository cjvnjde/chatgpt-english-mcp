import { test, mock } from 'node:test';
import assert from 'node:assert/strict';
import { completeChat } from '../api.js';
import { MCPClient } from '../mcp.js';
import { apiEndpoint, loadSettings, saveSettings, validateSettings } from '../settings.js';

const settings = { aiUrl: 'https://ai.example', aiKey: 'ai-secret', model: 'tutor', mcpUrl: 'https://dictionary.example/mcp', mcpToken: 'dictionary-secret' };
const messages = [{ role: 'user', content: 'Explain café.' }];
function stream(text) {
  const bytes = new TextEncoder().encode(text);
  return new Response(new ReadableStream({
    start(controller) {
      // Deliberately split CRLF pairs and multibyte Unicode across chunks.
      for (const byte of bytes) controller.enqueue(Uint8Array.of(byte));
      controller.close();
    },
  }), { headers: { 'Content-Type': 'text/event-stream' } });
}
function rpc(request, result, headers = {}) {
  return Response.json({ jsonrpc: '2.0', id: request.id, result }, { headers });
}

test('streamed explanations survive fragmented Unicode and CRLF without losing text', async t => {
  t.after(() => mock.restoreAll());
  mock.method(globalThis, 'fetch', async () => stream(
    ': heartbeat\r\ndata: {"choices":[{"index":0,"delta":{"content":"A café"}}]}\r\n\r\n' +
    'data: {"choices":[{"index":0,"delta":{"content":" is a coffee shop."},"finish_reason":"stop"}]}\r\n\r\n' +
    'data: [DONE]\r\n\r\n',
  ));
  const updates = [];
  assert.equal(await completeChat(settings, messages, { onDelta: text => updates.push(text) }), 'A café is a coffee shop.');
  assert.deepEqual(updates, ['A café', 'A café is a coffee shop.']);
});

test('truncated AI streams report failure rather than claim an incomplete answer succeeded', async t => {
  t.after(() => mock.restoreAll());
  mock.method(globalThis, 'fetch', async () => stream('data: {"choices":[{"delta":{"content":"Part of an answer"}}]}\n\n'));
  await assert.rejects(completeChat(settings, messages), /ended before completion/);
});

test('JSON-only compatible providers return a usable answer', async t => {
  t.after(() => mock.restoreAll());
  mock.method(globalThis, 'fetch', async () => Response.json({ choices: [{ message: { content: 'A coffee shop.' } }] }));
  assert.equal(await completeChat(settings, messages), 'A coffee shop.');
});

test('provider-default effort stays compatible and unsupported explicit effort fails without retry', async t => {
  t.after(() => mock.restoreAll());
  let calls = 0;
  mock.method(globalThis, 'fetch', async (_url, options) => {
    calls++;
    const body = JSON.parse(options.body);
    if (Object.hasOwn(body, 'reasoning_effort')) {
      if (body.reasoning_effort === 'low') return Response.json({ choices: [{ message: { content: 'A concise explanation.' } }] });
      return Response.json({ error: { message: 'Unsupported reasoning_effort' } }, { status: 400 });
    }
    return Response.json({ choices: [{ message: { content: 'A standard explanation.' } }] });
  });
  assert.equal(await completeChat(settings, messages), 'A standard explanation.');
  assert.equal(await completeChat({ ...settings, thinkingLevel: 'low' }, messages), 'A concise explanation.');
  await assert.rejects(completeChat({ ...settings, thinkingLevel: 'high' }, messages), /Unsupported reasoning_effort/);
  assert.equal(calls, 3);
});

test('provider failures do not expose credentials and cancellation remains distinguishable', async t => {
  t.after(() => mock.restoreAll());
  mock.method(globalThis, 'fetch', async () => Response.json({ error: { message: 'Rejected ai-secret and dictionary-secret' } }, { status: 401 }));
  await assert.rejects(completeChat(settings, messages), error => error.message.includes('401') && !error.message.includes('ai-secret') && !error.message.includes('dictionary-secret'));
  mock.restoreAll();
  mock.method(globalThis, 'fetch', (_url, { signal }) => new Promise((_resolve, reject) => {
    if (signal.aborted) reject(signal.reason);
    else signal.addEventListener('abort', () => reject(signal.reason), { once: true });
  }));
  const controller = new AbortController();
  const request = completeChat(settings, messages, { signal: controller.signal });
  controller.abort();
  await assert.rejects(request, { name: 'AbortError' });
});

test('MCP negotiated sessions survive streamed notifications and return tool data', async t => {
  t.after(() => mock.restoreAll());
  mock.method(globalThis, 'fetch', async (url, options) => {
    assert.equal(url, settings.mcpUrl);
    assert.equal(options.headers.Authorization, 'Bearer dictionary-secret');
    const request = JSON.parse(options.body);
    if (request.method === 'initialize') return rpc(request, { protocolVersion: '2025-06-18', capabilities: { tools: {} } }, { 'Mcp-Session-Id': 'session-1' });
    // This simulated stateful peer rejects a client that loses negotiation.
    if (options.headers['Mcp-Session-Id'] !== 'session-1' || options.headers['MCP-Protocol-Version'] !== '2025-06-18') return new Response('', { status: 400 });
    if (request.method === 'notifications/initialized') return new Response(null, { status: 202 });
    const data = { entries: [{ headword: 'café' }] };
    return stream('data: {"jsonrpc":"2.0","method":"notifications/message","params":{}}\r\n\r\n' +
      `data: ${JSON.stringify({ jsonrpc: '2.0', id: request.id, result: { content: [{ type: 'text', text: JSON.stringify(data) }] } })}\r\n\r\n`);
  });
  assert.deepEqual(await new MCPClient(settings).callTool('dictionary_lookup', { term: 'café' }), { entries: [{ headword: 'café' }] });
});

test('MCP tool errors and expired save sessions never masquerade as success or replay writes', async t => {
  t.after(() => mock.restoreAll());
  let saves = 0;
  mock.method(globalThis, 'fetch', async (_url, options) => {
    const request = JSON.parse(options.body);
    if (request.method === 'initialize') return rpc(request, { protocolVersion: '2025-03-26', capabilities: { tools: {} } });
    if (request.method === 'notifications/initialized') return new Response(null, { status: 202 });
    if (request.params.name === 'vocabulary_save') { saves++; return new Response(null, { status: 404 }); }
    return rpc(request, { isError: true, content: [{ type: 'text', text: 'Meaning not found' }] });
  });
  const client = new MCPClient(settings);
  await assert.rejects(client.callTool('dictionary_lookup', { term: 'unknown' }), /Meaning not found/);
  await assert.rejects(client.callTool('vocabulary_save', { term: 'unknown' }), /404/);
  assert.equal(saves, 1);
});

test('connection URL handling preserves custom routes and rejects credential-bearing URLs', () => {
  assert.equal(apiEndpoint('https://ai.example/api/v1/chat/completions', 'models'), 'https://ai.example/api/v1/models');
  assert.equal(apiEndpoint('https://ai.example', 'chat/completions'), 'https://ai.example/v1/chat/completions');
  assert.equal(apiEndpoint('https://ai.example/chat/completions', 'chat/completions'), 'https://ai.example/chat/completions');
  for (const aiUrl of ['http://remote.example', 'https://user:password@ai.example', 'https://ai.example?key=secret']) {
    assert.throws(() => validateSettings({ ...settings, aiUrl }));
  }
});

test('legacy no-context preference survives loading and independent mode edits', async t => {
  const previous = globalThis.browser;
  t.after(() => {
    if (previous === undefined) delete globalThis.browser;
    else globalThis.browser = previous;
  });
  let stored = { settings: { ...settings, contextMode: 'none' } };
  globalThis.browser = { storage: { local: {
    get: async () => structuredClone(stored),
    set: async value => { stored = structuredClone(value); },
  } } };
  const migrated = await loadSettings();
  assert.equal(migrated.quickContextMode, 'none');
  await saveSettings({ ...migrated, contextMode: 'page', thinkingLevel: 'high', quickThinkingLevel: 'low' });
  const reloaded = await loadSettings();
  assert.equal(reloaded.contextMode, 'page');
  assert.equal(reloaded.quickContextMode, 'none');
  assert.equal(reloaded.thinkingLevel, 'high');
  assert.equal(reloaded.quickThinkingLevel, 'low');
  assert.equal(validateSettings({ ...settings, contextMode: 'none', quickContextMode: 'surrounding' }).quickContextMode, 'surrounding');
});

test('multiline system prompts preserve whitespace without allowing multiline credentials or unbounded text', async t => {
  const previous = globalThis.browser;
  t.after(() => {
    if (previous === undefined) delete globalThis.browser;
    else globalThis.browser = previous;
  });
  let stored = {};
  globalThis.browser = { storage: { local: {
    get: async () => structuredClone(stored),
    set: async value => { stored = structuredClone(value); },
  } } };
  const systemPrompt = '  Explain the selected word.\r\n\tUse one example.\n';
  await saveSettings({ ...settings, systemPrompt, quickSystemPrompt: '' });
  const reloaded = await loadSettings();
  assert.equal(reloaded.systemPrompt, systemPrompt);
  assert.equal(reloaded.quickSystemPrompt, '');
  for (const field of ['aiKey', 'mcpToken', 'aiUrl']) {
    assert.throws(() => validateSettings({ ...settings, [field]: 'first\nsecond' }), error => error.field === field);
  }
  for (const field of ['systemPrompt', 'quickSystemPrompt']) {
    assert.throws(() => validateSettings({ ...settings, [field]: 'bad\u0000prompt' }), error => error.field === field);
    assert.throws(() => validateSettings({ ...settings, [field]: 'a'.repeat(32001) }), error => error.field === field);
  }
});
