import { apiEndpoint, validateSettings } from "./settings.js";

function safeMessage(message, settings) {
  let text = String(message);
  for (const secret of [settings.aiKey, settings.mcpToken]) {
    if (secret) text = text.split(secret).join("[redacted]");
  }
  return text.slice(0, 600);
}

function detail(payload) {
  return payload?.error?.message || payload?.message ||
    (typeof payload?.error === "string" ? payload.error : "");
}

async function request(settings, resource, { signal, timeout, body }, consume) {
  const controller = new AbortController();
  const abort = () => controller.abort(new DOMException("Request canceled.", "AbortError"));
  if (signal?.aborted) abort();
  else signal?.addEventListener("abort", abort, { once: true });
  const timer = setTimeout(() => controller.abort(new DOMException(
    `AI request timed out after ${timeout / 1000} seconds. Check the endpoint or try again.`, "TimeoutError",
  )), timeout);
  try {
    const headers = { Accept: body ? "text/event-stream, application/json" : "application/json" };
    if (settings.aiKey) headers.Authorization = `Bearer ${settings.aiKey}`;
    if (body) headers["Content-Type"] = "application/json";
    const response = await fetch(apiEndpoint(settings.aiUrl, resource), {
      method: body ? "POST" : "GET", headers,
      body: body ? JSON.stringify(body) : undefined,
      signal: controller.signal, credentials: "omit", redirect: "error", cache: "no-store",
      referrerPolicy: "no-referrer",
    });
    if (!response.ok) {
      let message = "";
      try { message = detail(JSON.parse(await response.text())); } catch { /* HTTP status remains the useful error. */ }
      const hint = response.status === 401 || response.status === 403
        ? " Check your AI API key and access permissions."
        : response.status === 404 ? " Check the AI base URL and model name."
          : response.status === 429 ? " The provider's rate or usage limit was reached." : "";
      throw new Error(`AI HTTP ${response.status} ${response.statusText}.${hint}${message ? ` ${message}` : ""}`);
    }
    return await consume(response);
  } catch (error) {
    if (controller.signal.aborted) throw controller.signal.reason;
    if (error instanceof TypeError) {
      throw new Error("Could not reach the AI endpoint. Check the URL, network, certificate, and whether the server redirects requests.");
    }
    throw new Error(safeMessage(error.message || error, settings));
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}

async function jsonResponse(response) {
  let payload;
  try { payload = JSON.parse(await response.text()); }
  catch { throw new Error("The AI endpoint returned malformed JSON. Check that this is an OpenAI-compatible API URL, not a web login page."); }
  if (payload?.error) throw new Error(`AI error: ${detail(payload) || "unspecified provider error"}`);
  return payload;
}

// Decode incrementally: neither UTF-8 characters nor SSE lines must align with chunks.
async function readEvents(response, onEvent) {
  if (!response.body) throw new Error("The AI endpoint returned an empty stream.");
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let data = [];
  let eventSize = 0;
  let stopped = false;
  const line = (value) => {
    if (value === "") {
      if (data.length) stopped = onEvent(data.join("\n")) === false;
      data = [];
      eventSize = 0;
    } else if (value === "data" || value.startsWith("data:")) {
      const part = value === "data" ? "" : value.slice(5).replace(/^ /, "");
      eventSize += part.length;
      if (eventSize > 2_000_000) throw new Error("The AI endpoint sent an oversized stream event.");
      data.push(part);
    }
  };
  const drain = (final) => {
    let start = 0;
    for (let index = 0; index < buffer.length && !stopped; index++) {
      const char = buffer[index];
      if (char !== "\n" && char !== "\r") continue;
      if (char === "\r" && index === buffer.length - 1 && !final) break;
      line(buffer.slice(start, index));
      if (char === "\r" && buffer[index + 1] === "\n") index++;
      start = index + 1;
    }
    buffer = buffer.slice(start);
    if (buffer.length > 2_000_000) throw new Error("The AI endpoint sent an oversized stream line.");
    if (final && !stopped) {
      if (buffer) line(buffer);
      line("");
    }
  };
  try {
    while (!stopped) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });
      drain(done);
      if (done) break;
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

function textContent(value) {
  if (typeof value === "string") return value;
  if (Array.isArray(value) && value.every(part => part?.type === "text" && typeof part.text === "string")) {
    return value.map(part => part.text).join("");
  }
  return null;
}

export async function listModels(candidate, { signal } = {}) {
  const settings = validateSettings(candidate);
  return request(settings, "models", { signal, timeout: 30_000 }, async response => {
    const payload = await jsonResponse(response);
    if (!Array.isArray(payload?.data) || payload.data.some(model => typeof model?.id !== "string")) {
      throw new Error("The models endpoint did not return an OpenAI model list (data with model IDs).");
    }
    return [...new Set(payload.data.map(model => model.id).filter(Boolean))].sort();
  });
}

export async function completeChat(candidate, messages, { signal, onDelta } = {}) {
  const settings = validateSettings(candidate);
  if (!settings.model) throw new Error("Choose or enter an AI model in settings first.");
  if (!Array.isArray(messages) || !messages.length || messages.some(message =>
    !["system", "user", "assistant"].includes(message?.role) || typeof message.content !== "string")) {
    throw new Error("Chat messages must contain a role and text content.");
  }
  const body = { model: settings.model, messages: messages.map(({ role, content }) => ({ role, content })), stream: true };
  if (settings.thinkingLevel) body.reasoning_effort = settings.thinkingLevel;
  return request(settings, "chat/completions", { signal, timeout: 120_000, body }, async response => {
    if (!response.headers.get("content-type")?.toLowerCase().includes("text/event-stream")) {
      const payload = await jsonResponse(response);
      const message = payload?.choices?.[0]?.message;
      const content = textContent(message?.content);
      if (!content) throw new Error(message?.refusal || "The AI endpoint returned no assistant text.");
      onDelta?.(content);
      return content;
    }
    let content = "";
    let finished = false;
    await readEvents(response, data => {
      if (data.trim() === "[DONE]") { finished = true; return false; }
      let payload;
      try { payload = JSON.parse(data); }
      catch { throw new Error("The AI endpoint returned malformed JSON in its event stream."); }
      if (payload?.error) throw new Error(`AI error: ${detail(payload) || "unspecified provider error"}`);
      if (!Array.isArray(payload?.choices)) throw new Error("The AI stream returned an event without completion choices.");
      const choice = payload.choices.find(item => item.index === 0) ?? payload.choices[0];
      if (!choice) return; // A final usage-only event has no choices.
      if (choice.finish_reason != null) finished = true;
      if (choice.delta?.refusal) throw new Error(choice.delta.refusal);
      const delta = textContent(choice.delta?.content ?? choice.message?.content);
      if (delta === null && choice.delta?.content != null) throw new Error("The AI stream returned non-text content.");
      if (delta) {
        content += delta;
        if (content.length > 2_000_000) throw new Error("The AI response exceeded the text size limit.");
        onDelta?.(content);
      }
    });
    if (!finished) throw new Error("The AI stream ended before completion. The partial response may be incomplete; try again.");
    if (!content) throw new Error("The AI endpoint completed without any assistant text.");
    return content;
  });
}
