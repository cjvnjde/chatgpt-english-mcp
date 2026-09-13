import { validateSettings } from "./settings.js";

const PROTOCOL_VERSIONS = ["2025-03-26", "2024-11-05", "2025-06-18", "2025-11-25"];

function object(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function errorText(value) {
  if (typeof value === "string") return value;
  if (object(value)) return `${value.code ? `${value.code}: ` : ""}${value.message || "Unknown MCP error"}`;
  return "Unknown MCP error";
}

async function rpcResponse(response, id) {
  const check = payload => {
    if (!object(payload) || payload.jsonrpc !== "2.0") throw new Error("MCP returned an invalid JSON-RPC message.");
    if (payload.id !== id) return undefined;
    if (payload.error) throw new Error(`MCP RPC error: ${errorText(payload.error)}`);
    if (!Object.hasOwn(payload, "result")) throw new Error("MCP response is missing its result.");
    return { value: payload.result };
  };
  if (!response.headers.get("content-type")?.toLowerCase().includes("text/event-stream")) {
    let payload;
    try { payload = JSON.parse(await response.text()); }
    catch { throw new Error("MCP returned malformed JSON. Use the exact Streamable HTTP endpoint, usually /mcp, not a web page."); }
    const result = check(payload);
    if (!result) throw new Error("MCP returned a response with a mismatched request ID.");
    return result.value;
  }
  if (!response.body) throw new Error("MCP returned an empty event stream.");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let data = [];
  let eventSize = 0;
  let result;
  const line = value => {
    if (value === "") {
      if (data.length) {
        let payload;
        try { payload = JSON.parse(data.join("\n")); }
        catch { throw new Error("MCP returned malformed JSON in its event stream."); }
        result = check(payload);
      }
      data = [];
      eventSize = 0;
    } else if (value === "data" || value.startsWith("data:")) {
      const part = value === "data" ? "" : value.slice(5).replace(/^ /, "");
      eventSize += part.length;
      if (eventSize > 4_000_000) throw new Error("MCP returned an oversized stream event.");
      data.push(part);
    }
  };
  const drain = final => {
    let start = 0;
    for (let index = 0; index < buffer.length && !result; index++) {
      const char = buffer[index];
      if (char !== "\n" && char !== "\r") continue;
      if (char === "\r" && index === buffer.length - 1 && !final) break;
      line(buffer.slice(start, index));
      if (char === "\r" && buffer[index + 1] === "\n") index++;
      start = index + 1;
    }
    buffer = buffer.slice(start);
    if (buffer.length > 4_000_000) throw new Error("MCP returned an oversized stream line.");
    if (final && !result) {
      if (buffer) line(buffer);
      line("");
    }
  };
  try {
    while (!result) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });
      drain(done);
      if (done) break;
    }
    if (!result) throw new Error("MCP event stream ended without a response to this request.");
    return result.value;
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

export class MCPClient {
  constructor(settings) {
    const normalized = validateSettings(settings);
    // Deliberately retain no AI credential in the MCP connection.
    this.url = normalized.mcpUrl;
    this.token = normalized.mcpToken;
    this.protocolVersion = PROTOCOL_VERSIONS[0];
    this.sessionId = null;
    this.nextId = 1;
    this.connected = false;
    this.connecting = null;
  }

  async connect({ signal } = {}) {
    if (signal?.aborted) throw new DOMException("Request canceled.", "AbortError");
    if (this.connected) return;
    if (this.connecting) return this.connecting;
    this.connecting = (async () => {
      const result = await this.request("initialize", {
        protocolVersion: PROTOCOL_VERSIONS[0],
        capabilities: {},
        clientInfo: { name: "english-dictionary-firefox", version: "1.0.0" },
      }, { signal });
      if (!object(result) || !PROTOCOL_VERSIONS.includes(result.protocolVersion)) {
        throw new Error("MCP server negotiated an unsupported protocol version.");
      }
      if (!object(result.capabilities?.tools)) throw new Error("This MCP server does not advertise tools support.");
      this.protocolVersion = result.protocolVersion;
      await this.request("notifications/initialized", undefined, { signal, notification: true });
      this.connected = true;
    })();
    try {
      await this.connecting;
    } catch (error) {
      this.sessionId = null;
      this.protocolVersion = PROTOCOL_VERSIONS[0];
      throw error;
    } finally {
      this.connecting = null;
    }
  }

  async request(method, params, { signal, notification = false } = {}) {
    if (!this.url) throw new Error("Enter your English MCP URL in settings first.");
    const controller = new AbortController();
    const abort = () => controller.abort(new DOMException("Request canceled.", "AbortError"));
    if (signal?.aborted) abort();
    else signal?.addEventListener("abort", abort, { once: true });
    const timer = setTimeout(() => controller.abort(new DOMException(
      "MCP request timed out after 45 seconds. Check your server or tunnel. A submitted save may already have completed; check before retrying.",
      "TimeoutError",
    )), 45_000);
    const id = notification ? undefined : this.nextId++;
    try {
      const headers = {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        "MCP-Protocol-Version": this.protocolVersion,
      };
      if (this.token) headers.Authorization = `Bearer ${this.token}`;
      if (this.sessionId) headers["Mcp-Session-Id"] = this.sessionId;
      const response = await fetch(this.url, {
        method: "POST", headers,
        body: JSON.stringify({ jsonrpc: "2.0", id, method, params }),
        signal: controller.signal, credentials: "omit", redirect: "error", cache: "no-store",
        referrerPolicy: "no-referrer",
      });
      if (!response.ok) {
        let message = "";
        try {
          const payload = JSON.parse(await response.text());
          message = errorText(payload.error || payload);
        } catch { /* An HTTP status is enough for non-JSON proxy errors. */ }
        let hint = "";
        if (response.status === 401 || response.status === 403) hint = " Check your MCP bearer token and access permissions.";
        if (response.status === 404) {
          hint = this.sessionId ? " The MCP session expired or the endpoint changed. Reconnect before retrying; requests are not replayed." : " Check the exact MCP endpoint path (usually /mcp).";
          this.connected = false;
          this.sessionId = null;
        }
        throw new Error(`MCP HTTP ${response.status} ${response.statusText}.${hint}${message ? ` ${message}` : ""}`);
      }
      if (method === "initialize") {
        const sessionId = response.headers.get("Mcp-Session-Id");
        if (sessionId && !/^[\x21-\x7e]+$/.test(sessionId)) throw new Error("MCP server returned an invalid session ID.");
        this.sessionId = sessionId;
      }
      if (notification) {
        await response.body?.cancel();
        return;
      }
      return await rpcResponse(response, id);
    } catch (error) {
      if (controller.signal.aborted) throw controller.signal.reason;
      if (error instanceof TypeError) {
        throw new Error("Could not reach the MCP endpoint. Check the URL, running server or tunnel, certificate, and redirects. Requests are not automatically retried.");
      }
      let message = String(error.message || error);
      if (this.token) message = message.split(this.token).join("[redacted]");
      throw new Error(message.slice(0, 800));
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
    }
  }

  async listTools({ signal } = {}) {
    await this.connect({ signal });
    const tools = [];
    const cursors = new Set();
    let cursor;
    do {
      const result = await this.request("tools/list", cursor ? { cursor } : {}, { signal });
      if (!object(result) || !Array.isArray(result.tools) || result.tools.some(tool => !object(tool) || typeof tool.name !== "string")) {
        throw new Error("MCP returned a malformed tool list.");
      }
      tools.push(...result.tools);
      cursor = result.nextCursor;
      if (cursor != null && (typeof cursor !== "string" || !cursor || cursors.has(cursor))) {
        throw new Error("MCP returned an invalid or repeated tool-list cursor.");
      }
      if (cursor) cursors.add(cursor);
      if (cursors.size > 100) throw new Error("MCP tool list exceeded 100 pages.");
    } while (cursor);
    return tools;
  }

  async callTool(name, args, { signal } = {}) {
    if (typeof name !== "string" || !name || !object(args)) throw new Error("MCP tools require a name and an arguments object.");
    await this.connect({ signal });
    const result = await this.request("tools/call", { name, arguments: args }, { signal });
    if (!object(result)) throw new Error("MCP returned a malformed tool result.");
    const text = Array.isArray(result.content)
      ? result.content.filter(item => item?.type === "text" && typeof item.text === "string").map(item => item.text).join("\n")
      : "";
    let parsed;
    if (text) {
      try { parsed = JSON.parse(text); } catch { /* Plain-text error content is valid MCP. */ }
    }
    if (result.isError) {
      let message = errorText(parsed || result.structuredContent || text || "Tool failed without an error message.");
      if (this.token) message = message.split(this.token).join("[redacted]");
      throw new Error(`MCP tool ${name} failed: ${message.slice(0, 700)}`);
    }
    if (object(result.structuredContent)) return result.structuredContent;
    if (object(parsed)) return parsed;
    throw new Error(`MCP tool ${name} did not return a structured object or a text JSON object.`);
  }
}
