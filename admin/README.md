# English MCP admin

A static SolidJS + TypeScript admin app. Vite produces `dist/`; nginx serves it and forwards `/admin/api/` to the existing Go service. There is no frontend server runtime, separate backend process, or direct browser connection to SQLite.

## Run with Docker Compose

Use the repository version containing the admin API and this frontend. Add a separate secret to your existing `.env`:

```sh
openssl rand -base64 32
```

Copy the result into `ADMIN_BEARER_TOKEN`. Keep `MCP_BEARER_TOKEN` and `ANKI_EXPORT_TOKEN` distinct. Then run:

```sh
docker compose -f docker-compose.yml -f docker-compose.admin.yml up -d --build
```

Open `http://localhost:8090` and enter `ADMIN_BEARER_TOKEN`. The default binding is localhost; for remote use, put the nginx service behind your existing HTTPS reverse proxy. Route your admin domain to `english-admin:80` on the shared Docker network. In a deployment platform that accepts only one Compose file, merge the two service definitions from `docker-compose.admin.yml` into your existing stack.

The admin container does not mount the database or receive the token. It forwards the token supplied by the browser. The Go API is available on the existing external listener (8081), never the unauthenticated tunnel listener (8080). An empty `ADMIN_BEARER_TOKEN` leaves the admin API disabled. The token grants database-wide read/export access; vocabulary mutations use the configured `MCP_OWNER_KEY` namespace. Do not use this single-administrator API for mutually untrusted users.

The token is held only in browser memory. Reloading or signing out clears the session. All API responses use `Cache-Control: no-store`; no credentials are embedded in the static bundle, URLs, localStorage, or sessionStorage. HTTPS must be supplied by the reverse proxy for remote access.

## Build only static files

```sh
cd admin
npm ci
npm run build
```

Host the contents of `admin/dist/` at your admin site's root. Configure your host to forward `/admin/api/` to `http://english-learning-mcp:8081/admin/api/`, preserving the `Authorization` header. The provided `nginx/default.conf.template` does this. `MCP_UPSTREAM` is nginx runtime configuration, so changing the upstream does not require rebuilding JavaScript.

The build contains only HTML, CSS, and JavaScript. Client navigation uses URL hashes, so static hosting needs no server-side page routes. API requests always use the same origin; no CORS changes are needed. The old MCP version cannot supply the admin endpoints: rebuild the Go service from this version too.

## Features and edit boundaries

| View | Capabilities |
| --- | --- |
| Vocabulary | Create words and meanings, inspect/edit status, description, notes, examples, tags, source, and usefulness hints; archive or delete with typed confirmation. |
| Reviews / Comments | Search by word, ID, comment, or any stored field; exact-column filters, ratings, dates, pagination; full before/after FSRS state and submitted/effective ratings. |
| Presentations | Inspect when a card was shown, selection kind, due date, and review token; follow links to cards and reviews. |
| Database | Every SQLite table, including dictionary snapshots, cards, migrations, usefulness state, and SQLite sequences; choose columns, inspect raw JSON/schema, and export a page or record. |
| Analytics | Status counts, due cards, submitted/effective rating distribution, 30-day activity, and difficult words for the configured owner. |
| Export database | Download the whole SQLite database as one `.sqlite` file, including all owners and system/history tables. |

Review and presentation tables remain immutable, as required by the existing database triggers. Cache, scheduler, and system tables are inspectable but not directly writable. Vocabulary writes use the existing service so validation, sense identity, usefulness inference, and card creation/deletion stay consistent. Term/sense identity is fixed after creation; create a new meaning when needed. The database stores review comments and ratings, not answer transcripts. Deleting a word removes its cards; historical records are retained and may no longer have a resolvable word label.

Notes and examples are edited separately to preserve multiline values. An empty array clears a list. Usefulness is inferred from offline evidence and the optional hint, so the result can differ from the selected hint. For existing items, an empty hint selector preserves the current hint; the existing service has no operation to clear it to automatic.

Search covers stored columns, plus the linked vocabulary term for history/cards. Exact filters match one selected column; date boundaries use UTC and the end day is inclusive. Table timestamps display in the browser's time zone. Database lists cover all owners; analytics and vocabulary mutations use the configured owner.

## SQLite export

The Export database button makes an authenticated request to `GET /admin/api/database`, receives a binary response, and starts a file download. It never includes the token in the URL.

The service uses [SQLite `VACUUM INTO`](https://sqlite.org/lang_vacuum.html#vacuuminto) to create a consistent, compact snapshot including committed WAL data. It writes into a private temporary directory, streams the resulting file, then removes the temporary snapshot. The original database is not modified. Only one export runs at a time. Export uses the service's existing SQLite connection, so other MCP database operations can briefly queue while the snapshot is generated. This is intended for the small development/admin databases described in this project.

The API allows 90 seconds to create an export; nginx and the client allow 120 seconds. The browser buffers the download as a Blob. Do not copy only the live `.sqlite` file while WAL mode is active. Keep downloads private: they contain the complete learning database, including cached source data and review tokens, but not environment-based MCP/admin/Anki secrets.

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

From the repository root:

```sh
go test ./...
```

The integration tests use temporary SQLite databases and exercise authorization, vocabulary CRUD and card lifecycle, query validation, read-only tables, and an exported file's signature, integrity, migration metadata, and committed data. Client tests cover credential handling, expired sessions, proxy misconfiguration, and binary downloads.

Production build and automated tests were run. Docker image execution and browser interaction tests require their respective runtimes and have not been run here.
