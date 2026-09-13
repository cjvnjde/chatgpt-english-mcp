// Model and page text never become HTML. Images need an exact dictionary URL.
function externalURL(value, httpsOnly = false) {
  try {
    const url = new URL(value);
    if (url.username || url.password || !["https:", ...(httpsOnly ? [] : ["http:"])].includes(url.protocol)) return null;
    return url.href;
  } catch {
    return null;
  }
}

function element(tag, text) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  return node;
}

function imageNode(alt, destination, options) {
  const url = externalURL(destination, true);
  const label = alt.trim() || "Dictionary illustration";
  if (!options.imageUrls.has(destination) || !url) {
    return document.createTextNode(`[${label} — image not loaded]`);
  }
  const wrapper = element("span");
  wrapper.className = "markdown-image";
  const image = element("img");
  image.alt = label;
  image.loading = "lazy";
  image.referrerPolicy = "no-referrer";
  const caption = element("span");
  caption.hidden = true;
  image.addEventListener("error", () => {
    image.remove();
    caption.hidden = false;
    caption.textContent = `${label} · Image unavailable`;
  }, { once: true });
  image.src = url;
  wrapper.append(image, caption);
  return wrapper;
}

// Find a Markdown link destination without truncating URLs containing parentheses.
function destinationAt(text, start) {
  let depth = 1;
  let end = start;
  for (; end < text.length; end++) {
    if (text[end] === "\\") { end++; continue; }
    if (text[end] === "(") depth++;
    if (text[end] === ")" && --depth === 0) break;
  }
  if (depth !== 0) return null;
  const raw = text.slice(start, end).trim();
  const match = /^(?:<([^<>\s]+)>|([^\s]+?))(?:\s+["'][^\n]*["'])?$/.exec(raw);
  return match ? { url: (match[1] || match[2]).replace(/\\([()\\])/g, "$1"), end: end + 1 } : null;
}

function inline(text, options, depth = 0) {
  const fragment = document.createDocumentFragment();
  if (depth > 12) { fragment.append(document.createTextNode(text)); return fragment; }
  let plain = "";
  const flush = () => { if (plain) fragment.append(document.createTextNode(plain)); plain = ""; };
  for (let i = 0; i < text.length;) {
    if (text[i] === "\\" && /[\\`*_{}\[\]()#+.!~>-]/.test(text[i + 1] || "")) {
      plain += text[i + 1]; i += 2; continue;
    }
    if (text[i] === "`") {
      const fence = /^`+/.exec(text.slice(i))[0];
      const end = text.indexOf(fence, i + fence.length);
      if (end !== -1) {
        flush(); fragment.append(element("code", text.slice(i + fence.length, end))); i = end + fence.length; continue;
      }
    }
    const isImage = text.startsWith("![", i);
    if (isImage || text[i] === "[") {
      const labelStart = i + (isImage ? 2 : 1);
      const labelEnd = text.indexOf("](", labelStart);
      const destination = labelEnd !== -1 ? destinationAt(text, labelEnd + 2) : null;
      if (destination) {
        flush();
        const label = text.slice(labelStart, labelEnd);
        if (isImage) fragment.append(imageNode(label, destination.url, options));
        else {
          const url = externalURL(destination.url);
          if (url) {
            const link = element("a"); link.href = url; link.target = "_blank"; link.rel = "noopener noreferrer";
            // Link labels are plain text: nested links/images cannot initiate extra requests.
            link.textContent = label || url; fragment.append(link);
          } else fragment.append(document.createTextNode(label || destination.url));
        }
        i = destination.end; continue;
      }
    }
    let matched = false;
    for (const [marker, tag] of [["**", "strong"], ["__", "strong"], ["~~", "del"], ["*", "em"], ["_", "em"]]) {
      if (!text.startsWith(marker, i)) continue;
      if (marker.includes("_") && /[\p{L}\p{N}]/u.test(text[i - 1] || "")) continue;
      const end = text.indexOf(marker, i + marker.length);
      if (end > i + marker.length) {
        flush(); const node = element(tag); node.append(inline(text.slice(i + marker.length, end), options, depth + 1));
        fragment.append(node); i = end + marker.length; matched = true; break;
      }
    }
    if (matched) continue;
    if (text[i] === "\n") { flush(); fragment.append(element("br")); i++; continue; }
    plain += text[i++];
  }
  flush();
  return fragment;
}

function blockStart(line) {
  return /^\s*$|^\s{0,3}(?:`{3,}|~{3,}|#{1,6}\s|>\s?|(?:[-+*]|\d+[.)])\s+|(?:[-*_]\s*){3,}$)/.test(line);
}

function blocks(text, options, depth = 0) {
  const fragment = document.createDocumentFragment();
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  for (let i = 0; i < lines.length;) {
    if (!lines[i].trim()) { i++; continue; }
    const fence = /^\s{0,3}(`{3,}|~{3,})(.*)$/.exec(lines[i]);
    if (fence) {
      const codeLines = []; i++;
      while (i < lines.length && !new RegExp(`^\\s{0,3}${fence[1][0]}{${fence[1].length},}\\s*$`).test(lines[i])) codeLines.push(lines[i++]);
      if (i < lines.length) i++;
      const pre = element("pre"); const code = element("code", codeLines.join("\n"));
      if (fence[2].trim()) pre.setAttribute("aria-label", `${fence[2].trim()} code`);
      pre.append(code); fragment.append(pre); continue;
    }
    const heading = /^\s{0,3}(#{1,6})\s+(.+?)\s*#*\s*$/.exec(lines[i]);
    if (heading) {
      const node = element(`h${Math.min(6, heading[1].length + 2)}`); node.append(inline(heading[2], options)); fragment.append(node); i++; continue;
    }
    if (/^\s{0,3}(?:[-*_]\s*){3,}$/.test(lines[i])) { fragment.append(element("hr")); i++; continue; }
    if (/^\s{0,3}>/.test(lines[i]) && depth < 8) {
      const quoted = [];
      while (i < lines.length && /^\s{0,3}>/.test(lines[i])) quoted.push(lines[i++].replace(/^\s{0,3}>\s?/, ""));
      const quote = element("blockquote"); quote.append(blocks(quoted.join("\n"), options, depth + 1)); fragment.append(quote); continue;
    }
    const firstItem = /^(\s{0,3})([-+*]|\d+[.)])\s+(.+)$/.exec(lines[i]);
    if (firstItem) {
      const ordered = /^\d/.test(firstItem[2]); const list = element(ordered ? "ol" : "ul");
      if (ordered && Number.parseInt(firstItem[2], 10) !== 1) list.start = Number.parseInt(firstItem[2], 10);
      while (i < lines.length) {
        const item = /^(\s{0,3})([-+*]|\d+[.)])\s+(.+)$/.exec(lines[i]);
        if (!item || /^\d/.test(item[2]) !== ordered) break;
        const body = [item[3]]; i++;
        while (i < lines.length && /^\s{2,}\S/.test(lines[i]) && !blockStart(lines[i])) body.push(lines[i++].trim());
        const li = element("li"); li.append(inline(body.join("\n"), options)); list.append(li);
      }
      fragment.append(list); continue;
    }
    const paragraph = [lines[i++]];
    while (i < lines.length && !blockStart(lines[i])) paragraph.push(lines[i++]);
    const node = element("p"); node.append(inline(paragraph.join("\n"), options)); fragment.append(node);
  }
  return fragment;
}

export function renderMarkdown(text, { imageUrls = [] } = {}) {
  return blocks(String(text ?? ""), { imageUrls: new Set(imageUrls.filter(url => typeof url === "string" && externalURL(url, true))) });
}
