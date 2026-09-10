# English MCP admin

A static SolidJS + TypeScript admin app. Vite produces `dist/`; nginx serves it and forwards `/admin/api/` to the existing Go service. There is no frontend server runtime, separate backend process, or direct browser connection to SQLite.

## Deploy with Dokploy

Use `docker-compose.yml` from the repository version containing the admin API and frontend. This single file includes the MCP, tunnel, Anki worker, and nginx admin service. Generate a separate admin secret:

```sh
openssl rand -base64 32
```

Copy the result into `ADMIN_BEARER_TOKEN` in Dokploy Environment. Keep `MCP_BEARER_TOKEN` and `ANKI_EXPORT_TOKEN` distinct. In Dokploy Domains, use the same host, `english-mcp.cjvnjde.tech`, for these routes:

| Path | Service | Container port | Strip Path | Internal Path |
| --- | --- | --- | --- | --- |
| `/mcp` | `english-learning-mcp` | `8081` | Off | Empty |
| `/admin` | `english-admin` | `80` | Off | Empty |

Enable HTTPS for the domain and deploy. Open `https://english-mcp.cjvnjde.tech/admin` and enter `ADMIN_BEARER_TOKEN`. nginx redirects `/admin` to `/admin/` using a relative redirect, so the HTTPS scheme and host are preserved. The admin route also covers its assets and `/admin/api/`; no separate API domain rule is required.

Keep the incoming paths intact: nginx serves the UI at `/admin/` and forwards `/admin/api/` unchanged to the MCP. Dokploy handles domain routing and TLS; the Compose file publishes no host ports or manual Traefik labels. All four containers share an explicit Compose network for internal communication, alongside the proxy networks Dokploy adds for Domains. See [Dokploy Domains](https://docs.dokploy.com/docs/core/domains) for path settings.

For an ordinary Compose deployment with your own reverse proxy, use the same file and routes:

```sh
docker compose up -d --build
```

The admin container does not mount the database or receive the token. It forwards the token supplied by the browser. The Go API is available on the existing external listener (8081), never the unauthenticated tunnel listener (8080). An empty `ADMIN_BEARER_TOKEN` leaves the admin API disabled. The token grants database-wide read/export access; vocabulary mutations use the configured `MCP_OWNER_KEY` namespace. Do not use this single-administrator API for mutually untrusted users.

The sign-in form includes an optional **Remember me on this device** checkbox, unchecked by default. After a successful sign-in, checking it saves the admin token in localStorage under `english-mcp.admin.token`, so it survives reloads and browser restarts. The app verifies the saved token with `/admin/api/session` before opening the workspace. Signing out or receiving HTTP 401 clears the saved token. Temporary connection/server failures keep it available for a retry. With Remember me unchecked, the token is held only in memory and reloading signs you out. If browser storage is blocked, sign-in still works for the current tab and the UI reports that the preference could not be saved.

The remembered token is readable by scripts on the same origin; use this option on your own trusted browser. All API responses use `Cache-Control: no-store`; no credentials are embedded in the static bundle, cookies, or URLs. HTTPS must be supplied by the reverse proxy for remote access.

## Build only static files

```sh
cd admin
npm ci
npm run build
```

Host the contents of `admin/dist/` under the public `/admin/` path. The included Dockerfile copies them into `/usr/share/nginx/html/admin`, matching the `/admin/` asset URLs emitted by Vite. Configure your host to forward `/admin/api/` to `http://english-learning-mcp:8081/admin/api/`, preserving the path and `Authorization` header. The provided `nginx/default.conf.template` does this. `MCP_UPSTREAM` is nginx runtime configuration, so changing the upstream does not require rebuilding JavaScript.

The build contains only HTML, CSS, and JavaScript. Client navigation uses URL hashes, so static hosting needs no server-side page routes. API requests always use the same origin; no CORS changes are needed. The old MCP version cannot supply the admin endpoints: rebuild the Go service from this version too.

## Features and edit boundaries

| View | Capabilities |
| --- | --- |
| Vocabulary | Create words and meanings, inspect/edit status, description, notes, examples, tags, source, usefulness hints, and independent personal interest; archive items or delete with typed confirmation. |
| Next suggestions | Preview words ranked by current next-draw probability, with selection reasons, learning groups, usefulness, due dates, and last-shown times; open words in the editor. |
| Reviews / Comments | Search by word, ID, comment, or any stored field; exact-column filters, ratings, dates, pagination; full before/after FSRS state and submitted/effective ratings. Comments stays restricted to commented reviews when optional filters are cleared. |
| Presentations | Inspect when a card was shown, selection kind, due date, and review token; follow links to cards and reviews. |
| Database | Every SQLite table, including dictionary snapshots, cards, migrations, usefulness state, and SQLite sequences; choose columns, inspect raw JSON/schema, and export a page or record. |
| Analytics | Status counts, due cards, submitted/effective rating distribution, 30-day activity, and difficult words for the configured owner. |
| Export database | Download the whole SQLite database as one `.sqlite` file, including all owners and system/history tables. |

Review and presentation tables remain immutable, as required by the existing database triggers. Cache, scheduler, and system tables are inspectable but not directly writable. Vocabulary writes use the existing service so validation, sense identity, usefulness inference, and card creation/deletion stay consistent. Term/sense identity is fixed after creation; create a new meaning when needed. The database stores review comments and ratings, not answer transcripts. Deleting a word removes its cards; historical records are retained and may no longer have a resolvable word label.

Notes, examples, and tags are edited as separate entries; notes and examples preserve multiline values, and commas within a tag are kept literally. An empty array clears a list. Usefulness is inferred from offline evidence and the optional hint, so the result can differ from the selected hint. For existing items, an empty hint selector preserves the current hint; the existing service has no operation to clear it to automatic.

Personal interest directly sets the learner's low/normal/high priority independently of inferred usefulness. It affects selection weights, not FSRS scheduling or Anki content.

Dirty drafts require explicit confirmation before closing, changing views, opening review history, or signing out. Reloading or leaving the page requests the browser's unsaved-changes confirmation. Drafts are kept in memory, not persisted across a confirmed reload.

The editor sends only changed fields with the loaded `expectedRevision`. If another writer changes the item, a 409 keeps the draft intact and pauses saving. **Load latest and merge draft** preserves local-only edits, adopts untouched remote fields, and requires a choice for fields changed on both sides. Description and attribution merge together. A changed usefulness hint requires an explicit choice because the saved hint is not returned by the vocabulary API. Explicit discard loads the latest saved item instead.

Admin vocabulary items include a `revision`. `PATCH /admin/api/vocabulary/{id}` requires a positive integer `expectedRevision`; missing, invalid, and stale values return 428, 400, and 409 respectively. MCP clients and Anki snapshots do not include this admin precondition.

Search covers stored columns, plus the linked vocabulary term for history/cards. Exact filters match one selected column; date boundaries use UTC and the end day is inclusive. Table timestamps display in the browser's time zone. Learning cards initially sort by earliest due date. Database lists cover all owners; suggestions, analytics, and vocabulary mutations use the configured owner.

## Next suggestions

The scheduler uses weighted random selection, not a fixed future queue. **Next suggestions** ranks active production cards by their probability of being chosen on the next `learning_next` call. It shares the actual scheduler's eligibility and weighting logic: cooldown and small-pool relaxation, due learning/relearning precedence, the 20% new / 80% mature-review mix when both pools remain available, and deterministic early-review fallback.

Ranks are likelihood ranks, not future turn numbers. Equal probabilities are displayed by due date and then card ID. Cards with no chance in the current snapshot appear after selectable cards with a reason such as cooling down, not due yet, or waiting for due learning steps. Archived items and other owners' cards are excluded.

Opening, refreshing, or paging through the preview does not record a presentation or review, rotate review tokens, or change scheduling state. The view refreshes after an editor save; use **Refresh** after external MCP activity or as time passes. Every request takes a fresh snapshot, so independently loaded pages can shift as learning state changes.

The authenticated `GET /admin/api/suggestions` endpoint accepts `limit` (1–200, default 50) and `offset` (nonnegative, default 0). Its response contains `owner`, `generatedAt`, `total` active cards, `selectable` cards with a positive next-draw chance, `limit`, `offset`, and `rows`. Each row includes `vocabularyItemId`, `cardId`, `term`, optional `context`, `status`, `usefulness`, `dueAt`, optional `lastShownAt`, `pool`, `probability` (0–1), and `reason`. An empty or past-end page returns `rows: []`.

## SQLite export

The Export database button makes an authenticated request to `GET /admin/api/database`, receives a binary response, and starts a file download. It never includes the token in the URL.

The service uses [SQLite `VACUUM INTO`](https://sqlite.org/lang_vacuum.html#vacuuminto) to create a consistent, compact snapshot including committed WAL data. It writes into a private temporary directory, streams the resulting file, then removes the temporary snapshot. The original database is not modified. Only one export runs at a time. Export uses the service's existing SQLite connection, so other MCP database operations can briefly queue while the snapshot is generated. This is intended for the small development/admin databases described in this project.

The API allows 90 seconds for an export request, including snapshot creation and streaming; nginx and the client allow 120 seconds. The browser buffers the download as a Blob. Do not copy only the live `.sqlite` file while WAL mode is active. Keep downloads private: they contain the complete learning database, including cached source data and review tokens, but not environment-based MCP/admin/Anki secrets.

To inspect a downloaded file:

```sh
sqlite3 english-mcp-YYYY-MM-DD.sqlite 'PRAGMA integrity_check;'
```

To restore, stop the MCP and any other database users first, preserve the old database plus any WAL/SHM files as a backup, then replace the database file with the downloaded snapshot and remove only the old WAL/SHM files from the live path. Preserve file ownership for UID/GID 10001 before restarting. Never replace a database while the service is running. There is intentionally no restore/upload endpoint in this UI.

## Local development and checks

Run the existing Go service with `ADMIN_BEARER_TOKEN` configured. Then:

```sh
cd admin
npm ci
ADMIN_DEV_UPSTREAM=http://localhost:8081 npm run dev
npm run build
npm test
```

Open the Vite development URL with `/admin/` appended (normally `http://localhost:5173/admin/`).

From the repository root:

```sh
go test ./...
```

The integration tests use temporary SQLite databases and exercise authorization, vocabulary CRUD and card lifecycle, query validation, read-only tables, and an exported file's signature, integrity, migration metadata, and committed data. Suggestion tests cover probability and random-selection boundaries, cooldown and learning precedence, early fallback, pagination, owner isolation, and unchanged learning state after preview reads. Client tests cover credential handling, expired sessions, proxy misconfiguration, binary downloads, remembered sign-in restoration and validation, sign-out cleanup, temporary server failures, and blocked browser storage.

For browser smoke checks of the built app, run `ADMIN_DEV_UPSTREAM=http://localhost:8081 npm run preview` and open `/admin/` on the printed URL. Exercise suggestion pagination, word editing, refresh, empty/error states, Comments filtering, and narrow-screen table scrolling. Browser interactions and Docker image execution are separate from `npm test`.
