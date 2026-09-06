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

## Package map

| Package | Responsibility |
|---|---|
| `cmd/english-learning-mcp` | Configuration, dependency wiring, two HTTP listeners, and graceful shutdown. |
| `internal/mcpserver` | MCP server metadata, strict schemas, tool registration, HTTP/auth middleware, and error conversion. |
| `internal/dictionary` | Cambridge HTTP client, HTML parser, cache policy, and lookup service. |
| `internal/vocabulary` | Validation and behavior for saved vocabulary and learner metadata. |
| `internal/learning` | Candidate presentation, FSRS scheduling, review ratings, and troublesome-item detection. |
| `internal/storage` | SQLite queries, transactions, cursor encoding, IDs, migrations, and domain hydration. |
| `internal/domain` | Shared dictionary, vocabulary, learning, and normalization types. |
| `internal/apperr` | Stable application error codes and safe client messages. |

## Request path

1. The MCP SDK receives a stateless JSON-response Streamable HTTP request on `/mcp`.
2. The registered tool applies schema defaults and validates the complete input object.
3. A domain service normalizes input and performs the operation.
4. Storage executes SQLite work, using transactions where multiple records must change atomically.
5. The result is validated against its declared output schema and returned as both text and structured MCP content.

Unexpected internal causes are logged server-side and replaced with a safe `INTERNAL_ERROR` message for clients.

## Storage behavior

SQLite uses foreign keys, WAL journal mode, a five-second busy timeout, `synchronous=NORMAL`, and one open connection. Important invariants include:

- one vocabulary item per owner and normalized term;
- one active dictionary snapshot per provider, term, dataset version, and parser version;
- one production card per vocabulary item;
- immutable review-attempt and presentation-event rows, retained after vocabulary deletion;
- unique, rotating review tokens;
- automatic learning-card creation for new or reactivated items.

`learning_next` is a mutating, non-destructive, non-idempotent operation. Candidate selection, vocabulary hydration, and presentation insertion share one SQLite transaction and connection. Each committed selection records owner/item/card IDs, exercise mode, review token, issuance and due timestamps, and selection kind. The response exposes the event ID and UTC issuance time; issuing an item does not modify FSRS scheduling state or rotate its review token.

Selection uses all due and FSRS-new cards, a last-three-presentations/30-minute cooldown, and bounded urgency, failure, and 24-hour recency weights. A 20% new-pool probability applies only when both pools remain after cooldown. Future cards are fallback-only. See [the selection policy](how-it-works.md#how-the-next-item-is-selected) for the small-pool rotation and future fallback rules.

Timestamp strings may have variable fractional precision: parse them as times before temporal comparisons rather than ordering their SQLite text lexically. Presentation history records server issuance, not guaranteed delivery or human visibility. Request retries append new events, potentially for different items. Review-token linkage can associate several presentations with one accepted review, but does not establish a recall duration. Migrations retain existing timestamps and never backfill invented presentation times.

## Migrations

SQL migrations live in `internal/storage/migrations/` and are embedded in the binary. Filenames must form an uninterrupted numeric sequence such as `006_description.sql`.

Never modify an applied migration. Its SHA-256 checksum is stored in `schema_migrations`, and a changed checksum prevents startup. Add a new migration instead and test both a fresh database and an upgrade from the previous schema.

## Adding or changing a tool

1. Add or update request/response types in `internal/mcpserver/types.go` or the relevant domain package.
2. Register the tool and its annotations in `internal/mcpserver/server.go`.
3. Keep business rules in a service package and persistence details in `internal/storage`.
4. Configure schema defaults and bounds explicitly where inference is insufficient.
5. Add success, validation, persistence, and idempotency tests as applicable.
6. Update [the tool reference](tools.md) and [workflow guide](how-it-works.md).

All new errors exposed to callers should use a stable `apperr` code and avoid leaking internal details.
