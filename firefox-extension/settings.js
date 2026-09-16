import { DEEP_SYSTEM_PROMPT, QUICK_SYSTEM_PROMPT } from "./prompt.js";

export const DEFAULT_SETTINGS = Object.freeze({
  aiUrl: "https://llm.cjvnjde.tech",
  aiKey: "",
  model: "",
  quickModel: "",
  thinkingLevel: "",
  quickThinkingLevel: "",
  systemPrompt: DEEP_SYSTEM_PROMPT,
  quickSystemPrompt: QUICK_SYSTEM_PROMPT,
  mcpUrl: "",
  mcpToken: "",
  contextMode: "surrounding",
  quickContextMode: "surrounding",
});

const LEGACY_BUNDLED_PROMPT_HASHES = Object.freeze({
  systemPrompt: "95a1dc2c8d3558486683676612558f252ef9729b171fd96f6d0cc634c6cc16ba",
  quickSystemPrompt: "a030fdd76cac3a494b9d93be1b7fd526ff57f4986365788edea7abdeda247642",
});

async function promptHash(value) {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return [...new Uint8Array(digest)].map(byte => byte.toString(16).padStart(2, "0")).join("");
}

async function migrateBundledPrompts(candidate) {
  if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) return { candidate, changed: false };
  let migrated = candidate;
  let changed = false;
  for (const [field, legacyHash] of Object.entries(LEGACY_BUNDLED_PROMPT_HASHES)) {
    const value = candidate[field];
    if (typeof value !== "string" || await promptHash(value) !== legacyHash) continue;
    if (!changed) migrated = { ...candidate };
    migrated[field] = DEFAULT_SETTINGS[field];
    changed = true;
  }
  return { candidate: migrated, changed };
}

function invalid(field, message) {
  const error = new Error(message);
  error.field = field;
  throw error;
}

function connectionUrl(value, field, label) {
  if (!value) return "";
  let url;
  try {
    url = new URL(value);
  } catch {
    invalid(field, `${label} must be a complete HTTPS URL (or HTTP on localhost).`);
  }
  const loopback = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback)) {
    invalid(field, `${label} must use HTTPS. HTTP is allowed only on a loopback host.`);
  }
  if (url.username || url.password || value.includes("?") || value.includes("#")) {
    invalid(field, `${label} cannot contain credentials, a query string, or a fragment.`);
  }
  return field === "mcpUrl" ? url.href : url.href.replace(/\/$/, "");
}

// Return a normalized, allowlisted object; incomplete setup is safe to save.
export function validateSettings(candidate) {
  if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) {
    throw new Error("Settings must be an object.");
  }
  const settings = {};
  for (const [field, fallback] of Object.entries(DEFAULT_SETTINGS)) {
    const fallbackValue = field === "quickContextMode" ? candidate.contextMode ?? fallback : fallback;
    const value = Object.hasOwn(candidate, field) ? candidate[field] : fallbackValue;
    if (typeof value !== typeof fallback) invalid(field, `Invalid value for ${field}.`);
    if (typeof value === "string") {
      const prompt = field === "systemPrompt" || field === "quickSystemPrompt";
      if ((prompt ? /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/ : /[\u0000-\u001f\u007f]/).test(value)) {
        invalid(field, `${field} cannot contain control characters.`);
      }
      if (prompt && value.length > 32000) invalid(field, "System prompts must be at most 32,000 characters.");
      settings[field] = prompt ? value : value.trim();
    } else {
      settings[field] = value;
    }
  }
  settings.aiUrl = connectionUrl(settings.aiUrl, "aiUrl", "AI base URL");
  settings.mcpUrl = connectionUrl(settings.mcpUrl, "mcpUrl", "English MCP URL");
  for (const field of ["contextMode", "quickContextMode"]) {
    if (!["none", "surrounding", "page"].includes(settings[field])) {
      invalid(field, "Choose a valid page context option.");
    }
  }
  for (const field of ["thinkingLevel", "quickThinkingLevel"]) {
    if (!["", "none", "minimal", "low", "medium", "high", "xhigh", "max"].includes(settings[field])) {
      invalid(field, "Choose a valid thinking effort.");
    }
  }
  return settings;
}

export async function loadSettings() {
  const stored = await browser.storage.local.get("settings");
  const migration = await migrateBundledPrompts(stored.settings ?? {});
  const settings = validateSettings(migration.candidate);
  if (migration.changed) await browser.storage.local.set({ settings });
  return settings;
}

export async function saveSettings(candidate) {
  const settings = validateSettings(candidate);
  await browser.storage.local.set({ settings });
  return settings;
}

export function apiEndpoint(base, resource) {
  const normalized = connectionUrl(typeof base === "string" ? base.trim() : "", "aiUrl", "AI base URL");
  if (!normalized) invalid("aiUrl", "Enter an AI base URL in settings first.");
  if (!["models", "chat/completions"].includes(resource)) throw new Error("Unsupported AI API resource.");
  const url = new URL(normalized);
  let path = url.pathname.replace(/\/+$/, "");
  if (!path) path = "/v1";
  else path = path.replace(/\/(?:chat\/completions|models)$/, "");
  url.pathname = `${path}/${resource}`;
  return url.href;
}
