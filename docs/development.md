# Development and architecture

## Requirements

- Go 1.27 or newer
- network access to Cambridge Dictionary for live lookups
- a writable filesystem location for SQLite

The SQLite driver is pure Go, so local development does not require CGO.

## Run locally

```sh
export SQLITE_PATH="$PWD/data/english-mcp.sqlite"
export MCP_BEARER_TOKEN="$(openssl rand -base64 32)"
export MCP_TUNNEL_LISTEN_ADDRESS="127.0.0.1:8080"
export MCP_EXTERNAL_LISTEN_ADDRESS="127.0.0.1:8081"
export LOG_FORMAT=text

go run ./cmd/english-learning-mcp
```

The unauthenticated local endpoint is `http://127.0.0.1:8080/mcp`; the endpoint on port `8081` requires the configured bearer token. Do not bind the unauthenticated listener to a public interface.

## Test and build

```sh
go test ./...
go build ./cmd/english-learning-mcp
```

## Offline frequency datasets

`internal/usefulness` embeds both complete English word-rank lists in a compressed binary table. The word-rank bundle covers 1,737,503 distinct normalized terms and adds approximately 15.8 MB to the executable, decoding to approximately 36.1 MB of immutable data. Separate expression assets provide Wiktionary, MAGPIE, and WordNet evidence and matching indexes. There is no runtime Python dependency or source download.

Refresh the checked-in assets with:

```sh
uv run --no-project --with msgpack scripts/build_usefulness.py
```

This maintenance command needs network access; ordinary builds and inference do not download frequency data. It downloads pinned, checksummed inputs, preserves source rank order, resolves normalization collisions to the best rank, and recreates `assets/ranks.bin.gz`, `assets/manifest.json`, `revision.go`, and verbatim upstream license files. Do not hand-edit those outputs. Before changing an input pin, verify its upstream release or dataset revision and update its checksum. The manifest records exact inputs, counts, and the decoded content checksum.

Expression assets have their own reproducible builder:

```sh
uv run --no-project --with msgpack scripts/build_expressions.py --cache-dir /path/to/expression-sources
uv run --offline --no-project --with msgpack scripts/build_expressions.py --offline --cache-dir /path/to/expression-sources
```

The first command downloads missing pinned inputs; the second requires cached Python dependencies and source inputs, and disables network access in both `uv` and the builder. The cached sources total roughly 2.9 GB, including the complete supported Kaikki raw export; processing streams the corpus and retains only English multiword records. Ordinary application builds need only the checked-in compressed assets, not these caches. The current expression index contains 218,249 canonical entries, 169,565 explicit aliases, and 2,321 genuine WordNet exception forms.

`assets/expressions-manifest.json` supplies the source, input-checksum, and legal-file configuration and records generated counts/checksums. To refresh a moving Kaikki snapshot, verify the new upstream snapshot and update those input pins deliberately; do not bypass a checksum failure or edit computed output fields. A pinned snapshot may no longer be available at its moving URL, so retain its source cache when offline regeneration is required. The builder supports `--output-dir` for independent reproduction without overwriting the checked-in outputs.

Run the builder regressions without loading the source corpus:

```sh
uv run --offline --no-project --with msgpack python -m unittest discover -s scripts -p 'test_*.py'
```

The builder retains unrestricted senses, collapses only unambiguous pure form/alternative entries, ignores false or unclear MAGPIE annotations, and uses only contiguous valid observed spans as aliases. Form-chain traversal stops at intermediate entries with independent lexical or usage evidence rather than bypassing them. Source POS gates first-verb inflection: a noun phrase such as `bank holiday` cannot become `banked holiday` merely because `bank` is also a verb. No regular inflections are fabricated. Corpus observations and tagged counts are attestation heuristics, not phrase-frequency ranks; the source `common` tag is deliberately not a positive-frequency signal.

Linguatools is not included: its commercial package requires a supplied data file and access terms. There is no placeholder adapter or runtime call to its API.

Both dataset checksums and the matching/scoring-policy version form the persisted inference revision. A data refresh or policy change must produce a new revision so startup recalculates existing vocabulary from original hints. No additional SQL migration is needed when only the evidence and matching policy change. Re-run the usefulness and migration tests after either change. The runtime Docker image also includes the upstream legal files from `internal/usefulness/licenses`.

## Package map

| Package | Responsibility |
|---|---|
| `cmd/english-learning-mcp` | Configuration, dependency wiring, HTTP listeners, and graceful shutdown. |
| `internal/mcpserver` | MCP server metadata, strict schemas, tool registration, HTTP/auth middleware, and error conversion. |
| `internal/dictionary` | Cambridge HTTP client, HTML parser, cache policy, and lookup service. |
| `internal/vocabulary` | Validation and behavior for saved vocabulary and learner metadata. |
| `internal/usefulness` | Embedded word ranks, expression evidence, conservative variant/typo matching, and weighted general-usefulness inference. |
| `internal/learning` | Candidate presentation, FSRS scheduling, review ratings, and troublesome-item detection. |
| `internal/storage` | SQLite queries, transactions, cursor encoding, IDs, migrations, and domain hydration. |
| `internal/domain` | Shared dictionary, vocabulary, learning, and normalization types. |
| `internal/apperr` | Stable application error codes and safe client messages. |
| `internal/ankiexport` | Private, export-only authenticated vocabulary snapshot endpoint. |
| `anki_worker` | Headless AnkiWeb adapter, persistent ownership tracking, authoritative reconciliation, and operator CLI. |

## Request path

1. The MCP SDK receives a stateless JSON-response Streamable HTTP request on `/mcp`.
2. The registered tool applies schema defaults and validates the complete input object.
3. A domain service normalizes input and performs the operation.
4. Storage executes SQLite work, using transactions where multiple records must change atomically.
5. The result is validated against its declared output schema and returned as both text and structured MCP content.

Unexpected internal causes are logged server-side and replaced with a safe `INTERNAL_ERROR` message for clients.

## Storage behavior

SQLite uses foreign keys, WAL journal mode, a five-second busy timeout, `synchronous=NORMAL`, and one open connection. Important invariants include:

- one vocabulary item per owner, normalized term, and sense key;
- one active dictionary snapshot per provider, term, dataset version, and parser version;
- one production card per vocabulary item;
- immutable review-attempt and presentation-event rows, retained after vocabulary deletion;
- unique, rotating review tokens;
- automatic learning-card creation for new or reactivated items.

`learning_next` is a mutating, non-destructive, non-idempotent operation. Candidate selection, vocabulary hydration, and presentation insertion share one SQLite transaction and connection. Each committed selection records owner/item/card IDs, exercise mode, review token, issuance and due timestamps, and selection kind. The response exposes the event ID and UTC issuance time; issuing an item does not modify FSRS scheduling state or rotate its review token.

Selection uses all due and FSRS-new cards, an adaptive last-three-presentations/30-minute cooldown, and bounded urgency, failure, and 24-hour recency weights. When the cooldown would force a rotation, older eligible cards can rejoin the lottery, preserving fresh alternatives and avoiding immediate repeats when alternatives exist. Future cards still identify the latest active presentation but are never made eligible by relaxation. Persisted vocabulary usefulness multiplies within-pool weights: low ×0.5, normal ×1, high ×2. When both pools remain, the 20:80 new/due baseline priority is multiplied by each pool's mean recency; usefulness and pool size do not directly alter that priority. Future selection remains fallback-only. See [the selection algorithm and exact probabilities](how-it-works.md#how-the-next-item-is-selected) for the decision flow, worked examples, cooldown relaxation, and future fallback rules, and [the scheduling equations](how-it-works.md#how-a-review-changes-the-schedule) for the separate FSRS model.

Timestamp strings have variable fractional precision. Compare parsed times, or compare canonical UTC timestamp prefixes with the terminal `Z` removed; raw RFC3339Nano text does not sort chronologically. Presentation history records server issuance, not guaranteed delivery or human visibility. Request retries append new events, potentially for different items. Review-token linkage can associate several presentations with one accepted review, but does not establish a recall duration. Migrations retain existing timestamps and never backfill invented presentation times.

Review persistence reads up to two presentations for the owner/card/token inside the review transaction, using a dedicated token index. Exactly one event supplies a parsed issuance time to the learning scheduler; missing or repeated events supply no timing evidence. A nonnegative interval of at most one minute promotes only `good` to `easy`. The effective rating is stored separately from the submitted rating so retry comparisons and comment history retain the original input. Duplicate lookup precedes timing evaluation and returns the persisted effective result without recomputing it.

## Migrations

SQL migrations live in `internal/storage/migrations/` and are embedded in the binary. Filenames must form an uninterrupted numeric sequence such as `006_description.sql`.

Never modify an applied migration. Its SHA-256 checksum is stored in `schema_migrations`, and a changed checksum prevents startup. Add a new migration instead and test both a fresh database and an upgrade from the previous schema.

Migration `008` originally added constrained vocabulary usefulness with a SQL default of `normal`. Migration `009` adds a nullable API hint and a scoring revision marker. It copies existing values into hints before the offline scorer recalculates effective usefulness. Application writes now calculate usefulness rather than relying on the SQL default.

The scoring revision covers bundled frequency data and the scoring policy. The migration transaction also performs revision-gated backfills across all owners and statuses; unchanged revisions skip the scan. Backfills update only effective usefulness and the revision marker, preserving vocabulary timestamps, cards, presentations, and reviews. Never feed effective usefulness back into the hint column. Changes to the scoring policy must advance its revision.

Migration `010` rebuilds vocabulary time-ordering and review-comment indexes over UTC timestamp prefixes without the terminal `Z`. Pagination and latest-comment queries preserve nanosecond ordering without rewriting stored timestamps or immutable history.

Migration `011` adds nullable `review_attempts.effective_rating` and a presentation-token lookup index. Legacy attempts are not rewritten: reads use their original rating when the effective rating is null. New reviews persist the actual FSRS rating. Existing immutability triggers remain in force.

## Adding or changing a tool

1. Add or update request/response types in `internal/mcpserver/types.go` or the relevant domain package.
2. Register the tool and its annotations in `internal/mcpserver/server.go`.
3. Keep business rules in a service package and persistence details in `internal/storage`.
4. Configure schema defaults and bounds explicitly where inference is insufficient.
5. Add success, validation, persistence, and idempotency tests as applicable.
6. Update [the tool reference](tools.md) and [workflow guide](how-it-works.md).

All new errors exposed to callers should use a stable `apperr` code and avoid leaking internal details.

## Anki integration

The optional private endpoint returns a complete owner-scoped snapshot from one SQLite read transaction. Its metadata includes schema version, stable namespace and owner, item count, explicit completeness, and a SHA-256 snapshot digest. The worker validates the entire snapshot before opening or changing Anki state. It never uses the paginated MCP list as a deletion authority.

Source identity combines integration namespace, encoded owner, and saved item ID. The worker keeps independent ownership mappings so a remote edit to the visible fields or deck cannot evade reconciliation. Every poll downloads remote state into the private worker collection, compares actual fields/tags with the source, and publishes corrections. The application database is never a sync destination.

Snapshot schema version `2` includes required usefulness metadata. The Python worker validates it but does not render it into Anki fields/tags or use it for Anki scheduling. Deploy the Go exporter and Python worker together; older snapshots are rejected instead of silently weakening validation. Existing source IDs, note mappings, and Anki cards remain unchanged.

Install the pinned library into an isolated environment:

```sh
uv venv --python 3.14.7 .venv
uv pip install --python .venv/bin/python -r anki_worker/requirements.txt
.venv/bin/python -W error::ResourceWarning -m unittest anki_worker.test_worker -v
```

Routine worker checks use temporary real Anki collections and controlled sync boundaries, not AnkiWeb credentials. They cover authoritative deletion, remote edits, card/review preservation, malformed exports, ownership collisions, shared-note protection, crash recovery, locks, and full-sync direction safety. Structure creation records temporary identities before allocating Anki IDs, so an interrupted first run can recover without claiming unrelated deck names.

The pinned runtime/library has been exercised against a disposable AnkiWeb account for login, empty-account bootstrap, existing-account full download, add/update/delete, remote repair, and restart. A complete Go HTTP export was also synced to AnkiWeb using the real worker CLI and polling loop. A real Anki review retained its card ID and review log after content repair; a before/after SQLite dump confirmed the application database did not change. See [the explicit integration procedure](deployment.md#version-upgrades-and-live-verification).
