# English Learning MCP

A self-hosted MCP server for looking up English terms, maintaining a personal vocabulary list, and scheduling production-recall reviews with FSRS.

It provides eleven tools for:

- Cambridge Dictionary lookups with permanent SQLite caching and server-side audio/image copies
- vocabulary metadata, attached example images, offline word/expression usefulness with optional AI hints, notes, examples, tags, and learning states
- read-only filtered vocabulary batches for AI sentence-writing and conversation exercises, without review tokens or schedule changes
- learning-step priority, adaptive new/mature-review shares, unseen and neglected-card priority, and recent-card cooldowns
- idempotent review recording, latest-answer correction, and spaced-repetition scheduling
- timestamped presentation history retained for future learning analytics
- learned-word reinforcement weighted by usefulness, comments, difficulty, and recency, with a strict 25% per-word probability cap
- personal-interest priorities that favor interesting words without excluding less-important ones

The MCP stores and schedules learning data; the connected AI tutor decides how to explain, quiz, and respond to the learner.

The Compose stack includes [one-way AnkiWeb sync](docs/deployment.md#ankiweb-sync), publishing saved vocabulary to a dedicated managed deck. Server content overrides Anki edits; Anki scheduling remains independent.

## Marketplace listing

- **Name:** English Vocabulary
- **Description:** Look up English words and expressions, save the meanings you want to learn, and build lasting vocabulary with personalized spaced-repetition reviews and contextual practice.
- **MCP server ID:** `english-learning-mcp`

The server advertises this display name and description in its MCP initialization response. Use the name and description above when creating your OpenAI marketplace listing; repository metadata does not publish a listing automatically.

Use the OpenAI tunnel connection for ChatGPT. Direct MCP clients can use your public HTTPS `/mcp` URL with `Authorization: Bearer <MCP_BEARER_TOKEN>`. Each deployment provides one shared vocabulary namespace, not separate accounts for marketplace users.

Dictionary images are delivered from saved SQLite copies as embedded MCP image content, through the existing MCP connection—no public image endpoint or Cambridge image hotlinks. Clients must support MCP image content to display them. Responses include at most four images and 8 MiB of decoded image data; unavailable images are skipped. Refresh older lookups with `dictionary_lookup` and `refresh: true` to download missing copies.

## Documentation

- [How it works: algorithms, formulas, diagrams, and workflows](docs/how-it-works.md)
- [MCP tool reference](docs/tools.md)
- [Suggested tutor, daily-review, and free-form exercise prompts](docs/prompts.md)
- [Configuration](docs/configuration.md)
- [Deployment](docs/deployment.md)
- [Development and architecture](docs/development.md)
- [Static admin UI, analytics, and SQLite export](admin/README.md)

## Quick start

```sh
cp .env.example .env
# Fill in the tunnel, MCP token, Anki export token, and AnkiWeb credentials.
docker compose up -d --build
```

The Streamable HTTP endpoint is `/mcp`. The Compose deployment exposes it to an OpenAI tunnel on internal port `8080` and to authenticated direct clients on internal port `8081`.

For Dokploy, select `docker-compose.yml`, copy the variables from `.env.example` into Environment, fill in the required values, and deploy. No additional Compose file is needed.

The same Compose file includes the static admin UI. Set `ADMIN_BEARER_TOKEN` and route `/admin` on your MCP domain to `english-admin:80`, preserving the path. Keep `/mcp` routed to `english-learning-mcp:8081`. See the [admin setup guide](admin/README.md) for the exact Dokploy domain settings.

## English Dictionary for Firefox

[`firefox-extension/`](firefox-extension/) is a personal Firefox-desktop extension (Firefox 142+) with quick explanations on the page and deeper, dictionary-backed explanations in the native sidebar. Only the sidebar’s Save icon adds vocabulary.

### Install and configure

1. Download `english-dictionary-<version>.xpi` from [GitHub Releases](https://github.com/cjvnjde/chatgpt-english-mcp/releases/latest). Use the `.xpi` asset, **not** the source-code ZIP/TAR archives.
2. In Firefox 142+, open `about:addons` → gear → **Install Add-on From File…** and select the downloaded `.xpi`. Accept the permission prompt.
3. Pin English Dictionary to the toolbar. Open settings using the sidebar gear or **Manage Extension → Preferences**.
4. Enter your OpenAI-compatible API URL and key. Use **Load models** to choose the **Deep · sidebar** model and **Quick · popup** model. An empty Quick model uses the Deep model, but keeps its own thinking effort, context, and prompt. No API key is bundled.
5. Enter your English MCP’s exact Streamable HTTP URL and separate bearer token. For this server, use your public HTTPS `/mcp` endpoint, or `http://127.0.0.1:8081/mcp` when running locally with `MCP_BEARER_TOKEN`.
6. Settings save automatically. Connection-test buttons are optional. AI-only explanations work without MCP; dictionary saving requires MCP.

Each mode has its own **Thinking effort**, **Page context**, and editable **System prompt**. Thinking defaults to the provider's choice; explicit levels require support for OpenAI-compatible `reasoning_effort` by your model/provider. Prompt editors show the complete bundled instructions, preserve multiline edits, and have independent **Reset** buttons. Fixed host rules remain visible and read-only: page/dictionary content is untrusted, models have no tools, and only Save can write vocabulary.

Select text and click the small dictionary icon. With the sidebar closed, only the quick model runs, without MCP. With it open, only the deep explanation runs. **Alt+Shift+E toggles the sidebar**: when closed, it opens and explains the pending selection; when open, it closes. The toolbar and **right-click → Explain in sidebar** remain open-only. Shortcut and toolbar capture use the focused page frame, including supported HTTP(S) iframes. Opening the sidebar dismisses the quick popup. Closing it cancels an unfinished deep response.

The quick action shows a spinner until text arrives, then displays the growing answer immediately with **Answering…** while streaming. A failed stream retains its partial text with an **Incomplete answer** notice and Retry. The sidebar also shows **Thinking…**, then **Answering…** while text streams. Indicators disappear when the response finishes or stops and respect reduced-motion preferences.

Firefox [does not permit a webpage button to open a closed native sidebar](https://developer.mozilla.org/en-US/docs/Mozilla/Add-ons/WebExtensions/User_actions); this is a browser restriction, not a permission you can grant. A content-script click sent through `runtime.sendMessage` does not give the background handler the required user activation. Toolbar, extension context-menu, and shortcut handlers can open it directly, before awaiting other work. Keyboard shortcuts can be reassigned in Firefox’s **Manage Extension Shortcuts** screen. On protected pages such as `about:` pages, Mozilla Add-ons, or Firefox’s PDF viewer, use manual input in the sidebar.

The sidebar shows the word, expandable context, answer, and follow-up input. The initial question is hidden; your follow-up messages are visible. Save first asks the model to choose the fitting dictionary definition and prepare an editable draft. Before any write, you can review the concise context cue, notes, examples, tags, personal interest, saved explanation, and source page. Context is limited to one–four short phrases and 400 characters rather than the raw page excerpt. Confirming calls MCP; an existing meaning keeps its item ID, learning schedule, and review history while the reviewed metadata, including context, is updated. A context-only item rejects an edit that would collide with another saved meaning of the same term. If no definition fits, the explanation can be saved without a dictionary sense. The current sidebar consumes dictionary text metadata but does not render embedded MCP image content. The sidebar follows the current Firefox window theme, with native system colors as its fallback; the settings page keeps its own interface styling.

Use the **Clear selected word and conversation** × button beside Save to start over without closing the sidebar. It clears the word, context, answer history, and current draft, stops pending explanations, and focuses the input for a new word. It affects only this window and does not delete saved vocabulary. A save still choosing a meaning or connecting to MCP is discarded; a write already sent to MCP cannot be undone by clearing. A follow-up correction also invalidates any unfinished save draft, so reopen Save after the new answer.

### Privacy and permanent installation

- Quick and Deep have independent context choices: selected words only (no page title or URL), up to 2,400 characters of surrounding visible text, or a selection-centered page excerpt of up to 12,000 characters. Opening Deep after Quick recaptures the original selection using Deep's choice; changed or unavailable source text falls back to the original term without stale context. On upgrade, Quick inherits the previous context setting, including None. URL query strings/fragments, hidden text, and form/editable fields are excluded.
- Editable-field selections are rejected even through the context menu; fallback text never overrides an explicit privacy denial. Frame-focus messages carry only frame identity, not selected text or page context.
- AI and MCP credentials are stored separately in local Firefox extension storage, **not encrypted** and not synced. Anyone with access to the Firefox profile can read them; password fields only hide them on screen. Protect the profile with OS access controls/full-disk encryption and use revocable, limited-scope tokens. Chats and captured context live only in background memory until cleared, replaced by a new selection, or the browser/extension restarts.
- Page content is sent only after an explicit explanation action. Quick mode sends context to AI only; Deep also sends the lookup term to MCP and supplies the returned dictionary facts to AI. Opening Save sends the explanation and page context to AI to prepare the editable draft but does not write vocabulary. Only confirmation sends MCP the selected definition and reviewed description, concise context, notes, examples, tags, personal interest, and source attribution; the raw page excerpt is never saved as vocabulary context. There is no telemetry. The MCP server no longer returns Cambridge image URLs; compatible MCP clients receive saved image bytes through their existing MCP connection.
- Host permissions allow selection actions on ordinary HTTP(S) pages and requests to configurable services. Service URLs must be HTTPS, except `localhost`, `127.0.0.1`, and `[::1]`. Configure final URLs: redirects are deliberately not followed.

The release XPI is signed by Mozilla for **unlisted/self-distributed installation**; no public AMO listing is needed. A ZIP containing the XPI is not itself an add-on: selecting that wrapper produces Firefox’s “corrupt” error. Do not rename or repack the release file. `SHA256SUMS` on the release page verifies its bytes. For local development only, use `about:debugging#/runtime/this-firefox` → **Load Temporary Add-on…** → `firefox-extension/manifest.json`; temporary installations disappear after Firefox restarts. See [Mozilla’s signing and distribution guide](https://extensionworkshop.com/documentation/publish/signing-and-distribution-overview/) and [development commands](docs/development.md#firefox-extension).

For automated releases, configure Mozilla API credentials in GitHub Actions secrets and push extension changes with a new manifest version to `main`. The **Release Firefox extension** workflow signs the version as unlisted and creates a GitHub Release with the direct XPI and checksum file. Reruns recover an already-approved version instead of resubmitting it. See the [release setup](docs/development.md#firefox-extension).


> This project retrieves data from Cambridge Dictionary and is not affiliated with or endorsed by Cambridge University Press & Assessment.
