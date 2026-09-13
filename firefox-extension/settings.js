export const DEFAULT_SETTINGS = Object.freeze({
  aiUrl: "https://llm.cjvnjde.tech",
  aiKey: "",
  model: "",
  quickModel: "",
  mcpUrl: "",
  mcpToken: "",
  contextMode: "surrounding",
});

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
    const value = Object.hasOwn(candidate, field) ? candidate[field] : fallback;
    if (typeof value !== typeof fallback) invalid(field, `Invalid value for ${field}.`);
    if (typeof value === "string") {
      if (/[\u0000-\u001f\u007f]/.test(value)) invalid(field, `${field} cannot contain control characters.`);
      settings[field] = value.trim();
    } else {
      settings[field] = value;
    }
  }
  settings.aiUrl = connectionUrl(settings.aiUrl, "aiUrl", "AI base URL");
  settings.mcpUrl = connectionUrl(settings.mcpUrl, "mcpUrl", "English MCP URL");
  if (!["none", "surrounding", "page"].includes(settings.contextMode)) {
    invalid("contextMode", "Choose a valid page context option.");
  }
  return settings;
}

export async function loadSettings() {
  const stored = await browser.storage.local.get("settings");
  return validateSettings(stored.settings ?? {});
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
