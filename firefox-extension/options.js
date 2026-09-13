import { DEFAULT_SETTINGS, loadSettings, saveSettings, validateSettings } from "./settings.js";
import { listModels, completeChat } from "./api.js";
import { MCPClient } from "./mcp.js";
import { HOST_SYSTEM_GUARD } from "./prompt.js";

const $ = id => document.getElementById(id);
const form = $("settingsForm");
const fields = Object.keys(DEFAULT_SETTINGS);
$("hostGuard").value = HOST_SYSTEM_GUARD;
const operations = new Map();
let revision = 0;
let ready = false;
let saves = Promise.resolve();

function status(id, text, kind = "info") {
  const element = $(id);
  element.textContent = text;
  element.dataset.kind = kind;
}

function fill(settings) {
  for (const field of fields) $(field).value = settings[field];
}

function validated() {
  for (const field of fields) $(field).removeAttribute("aria-invalid");
  return validateSettings(Object.fromEntries(fields.map(field => [field, $(field).value])));
}

function failure(error, statusId, focus = false) {
  if (error.name === "AbortError") {
    status(statusId, "");
    return;
  }
  if (error.field && fields.includes(error.field)) {
    $(error.field).setAttribute("aria-invalid", "true");
    if (focus) $(error.field).focus();
  }
  status(statusId, error.message || "The request failed.", "error");
}

function autosave(reportErrors = false) {
  if (!ready) return;
  let settings;
  try {
    settings = validated();
  } catch (error) {
    if (reportErrors) failure(error, "saveStatus");
    return;
  }
  const startedRevision = revision;
  saves = saves.then(async () => {
    if (revision !== startedRevision) return;
    await saveSettings(settings);
    if (revision === startedRevision) status("saveStatus", "");
  }).catch(error => {
    if (revision === startedRevision) failure(error, "saveStatus");
  });
}

async function runOperation(name, buttonId, cancelId, statusId, action) {
  if (operations.has(name)) return;
  const controller = new AbortController();
  const startedRevision = revision;
  operations.set(name, controller);
  $(buttonId).disabled = true;
  $(buttonId).setAttribute("aria-busy", "true");
  $(cancelId).hidden = false;
  status(statusId, "");
  try {
    const result = await action(validated(), controller.signal);
    controller.signal.throwIfAborted();
    if (revision === startedRevision) status(statusId, result, "success");
  } catch (error) {
    if (revision === startedRevision) failure(error, statusId, true);
  } finally {
    operations.delete(name);
    $(buttonId).disabled = false;
    $(buttonId).removeAttribute("aria-busy");
    const restoreFocus = document.activeElement === $(cancelId);
    $(cancelId).hidden = true;
    if (restoreFocus) $(buttonId).focus();
  }
}

for (const [name, id] of [["models", "cancelModels"], ["ai", "cancelAi"], ["mcp", "cancelMcp"]]) {
  $(id).addEventListener("click", () => operations.get(name)?.abort());
}

$("loadModels").addEventListener("click", () => runOperation(
  "models", "loadModels", "cancelModels", "modelsStatus",
  async (settings, signal) => {
    const models = await listModels(settings, { signal });
    signal.throwIfAborted();
    $("modelList").replaceChildren(...models.map(model => {
      const option = document.createElement("option");
      option.value = model;
      return option;
    }));
    if (!models.length) throw new Error("No models available.");
    return `${models.length} model${models.length === 1 ? "" : "s"}.`;
  },
));

$("testAi").addEventListener("click", () => runOperation(
  "ai", "testAi", "cancelAi", "aiStatus",
  async (settings, signal) => {
    await completeChat(settings, [{ role: "user", content: "Say hello in one short sentence." }], { signal });
    return "AI connected.";
  },
));

$("testMcp").addEventListener("click", () => runOperation(
  "mcp", "testMcp", "cancelMcp", "mcpStatus",
  async (settings, signal) => {
    const client = new MCPClient(settings);
    await client.connect({ signal });
    const tools = await client.listTools({ signal });
    const names = new Set(tools.map(tool => tool.name));
    const missing = ["dictionary_lookup", "vocabulary_save"].filter(name => !names.has(name));
    if (missing.length) throw new Error(`Missing MCP tools: ${missing.join(", ")}.`);
    return "MCP connected.";
  },
));

for (const button of document.querySelectorAll("[data-reveal]")) {
  button.addEventListener("click", () => {
    const input = $(button.dataset.reveal);
    const show = input.type === "password";
    input.type = show ? "text" : "password";
    button.textContent = show ? "Hide" : "Show";
    button.setAttribute("aria-pressed", String(show));
    button.setAttribute("aria-label", `${show ? "Hide" : "Show"} ${input.id === "aiKey" ? "AI API key" : "MCP bearer token"}`);
  });
}

function settingsChanged(field, reportErrors = false) {
  if (!ready || !fields.includes(field)) return;
  revision++;
  for (const controller of operations.values()) controller.abort();
  for (const id of ["saveStatus", "modelsStatus", "aiStatus", "mcpStatus"]) status(id, "");
  if (field === "aiUrl" || field === "aiKey") $("modelList").replaceChildren();
  autosave(reportErrors);
}

for (const button of document.querySelectorAll("[data-reset]")) {
  button.addEventListener("click", () => {
    if (!ready) return;
    const field = button.dataset.reset;
    $(field).value = DEFAULT_SETTINGS[field];
    settingsChanged(field, true);
  });
}

form.addEventListener("input", event => settingsChanged(event.target.id));
form.addEventListener("change", event => settingsChanged(event.target.id, true));

form.addEventListener("submit", event => {
  event.preventDefault();
  autosave(true);
});

window.addEventListener("pagehide", () => {
  for (const controller of operations.values()) controller.abort();
});

try {
  fill(await loadSettings());
} catch (error) {
  fill(DEFAULT_SETTINGS);
  status("saveStatus", `Could not load settings: ${error.message}`, "error");
} finally {
  ready = true;
  $("formFields").disabled = false;
}
