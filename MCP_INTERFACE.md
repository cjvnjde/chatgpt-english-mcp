# English Learning MCP Interface

**Status:** implemented; this document defines the version 1 interface and deployment contract.

## 1. Goal

Expose the useful parts of `english_dictionary` as a small MCP interface that can:

- retrieve source-backed dictionary data for a word, phrase, idiom, or expression;
- cache dictionary responses;
- store context-specific learner explanations;
- save and remove words or phrases from a learner's vocabulary;
- list saved vocabulary items and cached explanations;
- retrieve one saved vocabulary item or explanation without returning unrelated data.

In this document, **term** means any lookup target: a single word, phrase, idiom, phrasal verb, or expression.

## 2. Findings from the Telegram bot

The current bot has three different kinds of state that should not be collapsed into one MCP object:

1. **Source dictionary data**
   - `DictionaryLookup` contains entries, definitions, examples, phrases, labels, related words, synonyms, antonyms, pronunciations, audio URLs, images, idioms, collocations, and suggestions.
   - It is globally reusable and currently cached by normalized lookup text in `dictionary_lookups`.
   - Relevant code: `src/types.ts`, `src/services/cambridge.ts`, and `src/db.ts`.

2. **A learner explanation for one context and meaning**
   - The AI parses a request, selects a dictionary entry and definition, explains why it fits the context, adds learner notes/examples, resolves CEFR, and may add missing lexical relations.
   - The same spelling can therefore have several valid cached explanations. For example, `bank` with a financial context is not the same cache entry as `bank` beside a river.
   - Relevant code: `processLookupRequest`, `AiExplanation`, `ComplexityLevel`, and the structured values stored in `ai_responses`.

3. **A saved vocabulary item**
   - `/words` currently derives saved vocabulary by grouping successful Telegram requests. This couples the vocabulary collection to chat history.
   - MCP should instead store an explicit vocabulary-item record. Looking up or caching an explanation must not silently save the term.

Telegram-specific data is not part of the MCP domain: chat/message IDs, callback state, reply formatting, message deletion, and inline keyboard settings should stay out of the interface.

## 3. Main design decision

### MCP owns facts and persistence; the MCP client model owns AI reasoning

The initial MCP server should **not call OpenRouter internally**. The connected model already provides the AI layer. A normal explanation flow is:

1. call `explanation_get` to check the context-specific explanation cache;
2. on a miss, call `dictionary_lookup` for source facts;
3. select the source meaning and create a learner explanation;
4. call `explanation_write` to cache the structured explanation;
5. call `vocabulary_save` only when the user asks to save the term.

This replaces the bot's preprocessing, sense-selection, CEFR-estimation, grammar-label, combined-explanation, and lexical-enrichment model calls with the MCP client's model. It avoids a nested AI request, a second model configuration, duplicate cost, and opaque model-to-model behavior.

The boundary is strict:

- source definitions/examples are returned by `dictionary_lookup` and are never accepted back as model-authored text;
- `explanation_write` references a specific immutable dictionary snapshot and selected entry/definition indexes;
- learner-authored or AI-authored content is stored separately from source content;
- source and generated provenance remain distinguishable.

If server-owned AI is preferred later, a convenience `word_explain` tool can orchestrate the same public primitives. It should not replace them.

## 4. MCP surface

Version 1 exposes tools only. MCP resources and prompts add no capability to these parameterized and paginated operations.

| Tool | Side effect | Purpose |
|---|---:|---|
| `dictionary_lookup` | Cache only | Fetch provider data through a read-through cache. |
| `vocabulary_save` | Yes | Idempotently add a word or phrase to the learner's collection. |
| `vocabulary_get` | No | Get one saved vocabulary item and its explanation summaries. |
| `vocabulary_list` | No | List/filter saved vocabulary items with cursor pagination. |
| `vocabulary_delete` | Yes | Remove an item from the collection without silently evicting caches. |
| `explanation_get` | No | Retrieve one full context-specific cached explanation. |
| `explanations_list` | No | List explanation summaries independently of saved vocabulary. |
| `explanation_write` | Yes | Upsert or delete a cached learner explanation. |

The surface intentionally separates read tools from destructive tools. The only combined operation is `explanation_write`, where `op` makes the mutation explicit and keeps one schema for explanation ownership and validation.

## 5. Protocol conventions

- Tool names use `snake_case`; JSON fields use `camelCase` to match the TypeScript domain types.
- Every successful tool result is one JSON object with no surrounding prose. An implementation may return it as MCP `structuredContent`; when text compatibility is required, the text block contains the same object serialized as JSON.
- Every input object rejects unknown fields.
- IDs are opaque strings. Clients must not parse or construct them.
- Timestamps are ISO 8601 UTC strings.
- Optional values are omitted rather than returned as `null`, except where `null` has domain meaning such as `selectedMeaning: null` for an AI-only fallback.
- List tools use opaque cursor pagination. `limit` defaults to `50`, with an allowed range of `1..100`.
- List results contain summaries. A full explanation is returned only by `explanation_get`.
- Owner identity is derived from the authenticated MCP connection or deployment configuration. A model must never pass `userId`, `chatId`, or `ownerKey`.
- Raw dictionary cache is shared because it contains public source data. Saved vocabulary and explanations are owner-scoped because contexts and learner content may be private.
- Expected misses are data, not protocol errors. `explanation_get` returns `found: false`; it does not fail the tool call.

### Error codes

Invalid or failed calls return MCP tool errors with one stable application code:

- `INVALID_ARGUMENT` — malformed term, invalid cursor, incompatible fields, or invalid entry/definition index;
- `NOT_FOUND` — requested vocabulary item, explanation ID, or lookup snapshot does not exist;
- `STALE_LOOKUP` — an explanation write references a snapshot that is no longer accepted for new cache entries;
- `UPSTREAM_ERROR` — dictionary provider failed and no usable cached snapshot exists;
- `UNAUTHORIZED` — the authenticated principal cannot access the owner-scoped record;
- `INTERNAL_ERROR` — unexpected storage or serialization failure.

Errors must be actionable and must not include secrets, raw upstream HTML, prompts, or API keys.

## 6. Shared output structures

The declarations below describe JSON shapes, not implementation-language exports.

```ts
type CacheState = "hit" | "miss" | "refreshed" | "stale_fallback";
type CefrLevel = "A1" | "A2" | "B1" | "B2" | "C1" | "C2";

type SourceRef = {
  provider: string;          // "cambridge" initially; provider-neutral contract
  sourceUrl?: string;
  datasetVersion?: string;
  parserVersion: number;
};

type DictionaryAudio = {
  audioUrl: string;
  contentType: string;
};

type DictionaryImage = {
  title?: string;
  alt?: string;
  imageUrl: string;
  thumbnailUrl?: string;
  credit?: string;
};

type DictionaryWordGroup = {
  topic?: string;
  words: string[];
};

type DictionaryDefinition = {
  definition: string;
  examples: string[];
  phrases: string[];
  seeAlso: string[];
  images: DictionaryImage[];
  guideword?: string;
  phraseTitle?: string;
  labels: string[];
  usages?: Array<{ phrase?: string; example?: string }>;
  related?: Array<{ topic?: string; words: string[] }>;
  synonyms?: DictionaryWordGroup[];
  antonyms?: DictionaryWordGroup[];
};

type DictionaryEntry = {
  headword: string;
  partOfSpeech?: string;
  pronunciations: { uk?: string; us?: string };
  audio?: { us?: DictionaryAudio; uk?: DictionaryAudio };
  inflections?: string[];
  definitions: DictionaryDefinition[];
  idioms?: string[];
};

type DictionaryLookupResult = {
  lookupId: string;          // identifies this immutable source snapshot
  requestedTerm: string;
  normalizedTerm: string;
  cache: {
    state: CacheState;
    fetchedAt: string;
    expiresAt: string;
  };
  source: SourceRef;
  status: number;
  entries: DictionaryEntry[];
  suggestions: string[];
  images: DictionaryImage[];
  idioms?: string[];
  collocations?: Array<{ phrase: string; example?: string }>;
};

type ExplanationSummary = {
  explanationId: string;
  term: string;
  normalizedTerm: string;
  context: string;
  partOfSpeech?: string;
  cefr?: {
    level: CefrLevel;
    source: "dictionary" | "ai";
  };
  descriptionPreview: string;
  stale: boolean;
  createdAt: string;
  updatedAt: string;
};

type VocabularyItem = {
  itemId: string;
  term: string;
  normalizedTerm: string;
  explanationCount: number;
  cefrLevels: CefrLevel[];
  createdAt: string;
  updatedAt: string;
};

type Explanation = {
  explanationId: string;
  term: string;
  normalizedTerm: string;
  context: string;
  lookupId: string;
  selectedMeaning: null | {
    entryIndex: number;
    definitionIndex: number;
    headword: string;              // derived by the server
    partOfSpeech?: string;         // derived by the server
    definition: string;            // exact source text, derived by the server
    examples: string[];            // exact source examples, derived by the server
    labels: string[];              // derived by the server
  };
  learner: {
    description: string;
    whyThisMeaningFits?: string;
    notes: string[];
    examples: string[];            // generated learner examples, not source examples
    alternatives: Array<{
      partOfSpeech?: string;
      explanation: string;
      reason?: string;
      confidence?: number;         // 0..1
    }>;
  };
  cefr?: {
    level: CefrLevel;
    source: "dictionary" | "ai";
    confidence?: number;           // allowed only for source="ai"
    reason?: string;
  };
  lexicalRelations?: {
    synonyms: string[];
    antonyms: string[];
    source: "dictionary" | "ai" | "mixed";
  };
  generator: {
    name: string;                  // e.g. "chatgpt"
    model?: string;
    version: string;               // explanation/prompt contract version
  };
  stale: boolean;
  createdAt: string;
  updatedAt: string;
};
```

`stale` means the explanation remains available as history but its `lookupId` or source parser version is no longer current. Stale explanations are not returned as normal cache hits unless explicitly requested.

## 7. Tool contracts

### 7.1 `dictionary_lookup`

Read through the source dictionary cache. This tool performs no AI work and does not save the term to the learner's collection.

#### Input

```ts
{
  term: string;             // required; trimmed word/phrase/idiom/expression
  refresh?: boolean;        // default false; bypass active cache and fetch a new snapshot
}
```

Constraints:

- `term` must be 1–200 Unicode characters after trimming and whitespace normalization.
- The caller passes only the lookup target, not a whole sentence. Context belongs in explanation tools.
- `refresh: true` is an explicit upstream request. It creates a new immutable snapshot rather than mutating an old snapshot in place.

#### Output

`DictionaryLookupResult`.

A valid dictionary miss is a successful result with `entries: []`, the provider HTTP `status`, and any `suggestions`. It is not an MCP error.

### 7.2 `vocabulary_save`

Idempotently save a word, phrase, idiom, phrasal verb, or expression. Saving the same normalized term again returns the existing record and does not create a duplicate.

#### Input

```ts
{
  term: string;
}
```

#### Output

```ts
{
  created: boolean;
  item: VocabularyItem;
}
```

The display `term` is initially the trimmed input. If a source-backed explanation later identifies a canonical headword, the implementation may use that headword only when it normalizes to the same key; it must not merge semantically different phrases.

### 7.3 `vocabulary_get`

Get one saved vocabulary item. Exactly one identifier is required.

#### Input

```ts
{
  itemId?: string;
  term?: string;
  includeStaleExplanations?: boolean; // default false
}
```

#### Output

```ts
{
  item: VocabularyItem;
  explanations: ExplanationSummary[]; // newest first
}
```

The tool returns `NOT_FOUND` when the term exists only in dictionary/explanation cache but is not saved.

### 7.4 `vocabulary_list`

List saved vocabulary items for the authenticated owner.

#### Input

```ts
{
  query?: string;             // case-insensitive term substring/prefix search
  cefr?: CefrLevel[];         // OR within the supplied levels
  hasExplanation?: boolean;
  sort?: "recent" | "oldest" | "alphabetical"; // default "recent"
  limit?: number;             // default 50, maximum 100
  cursor?: string;
}
```

#### Output

```ts
{
  items: VocabularyItem[];
  nextCursor?: string;
}
```

`recent` sorts by `updatedAt` descending with `itemId` as a deterministic tie-breaker. CEFR filtering uses non-stale explanations only.

### 7.5 `vocabulary_delete`

Remove a saved vocabulary item from the learner's collection.

#### Input

```ts
{
  itemId: string;
}
```

#### Output

```ts
{
  deleted: true;
  itemId: string;
}
```

This operation does **not** delete raw dictionary snapshots or cached explanations. Those caches may still serve future requests and can be managed explicitly with `explanation_write`. This avoids turning an ordinary “remove from my list” request into hidden cache destruction.

### 7.6 `explanation_get`

Retrieve one full cached explanation either by ID or by its natural cache key. Exactly one lookup mode is required.

#### Input

```ts
type ExplanationGetInput =
  | {
      explanationId: string;
      includeStale?: boolean;      // default false
    }
  | {
      term: string;
      context?: string;            // default ""
      generator: {
        name: string;
        version: string;
      };
      includeStale?: boolean;      // default false
    };
```

#### Output

```ts
type ExplanationGetOutput =
  | {
      found: true;
      explanation: Explanation;
    }
  | {
      found: false;
      normalizedTerm: string;
      normalizedContext: string;
      reason: "not_cached" | "stale_only";
    };
```

Natural-key lookup selects the newest explanation for the exact normalized term, context, generator name, and generator version. It must reference the active dictionary snapshot unless `includeStale` is true. It must never use a context-free explanation as a hit for a contextual request, or vice versa.

### 7.7 `explanations_list`

List cached explanation summaries. This includes explanations for unsaved terms unless `onlySavedItems` is true.

#### Input

```ts
{
  term?: string;              // exact normalized term filter
  itemId?: string;            // resolves to the saved item's normalized term
  cefr?: CefrLevel[];
  onlySavedItems?: boolean;   // default false
  includeStale?: boolean;     // default false
  sort?: "recent" | "oldest" | "alphabetical"; // default "recent"
  limit?: number;             // default 50, maximum 100
  cursor?: string;
}
```

`term` and `itemId` are mutually exclusive.

#### Output

```ts
{
  explanations: ExplanationSummary[];
  nextCursor?: string;
}
```

### 7.8 `explanation_write`

Upsert or delete an owner-scoped learner explanation.

#### Upsert input

```ts
{
  op: "upsert";
  value: {
    term: string;
    context?: string;               // default ""
    lookupId: string;
    selectedMeaning: null | {
      entryIndex: number;
      definitionIndex: number;
    };
    learner: {
      description: string;
      whyThisMeaningFits?: string;
      notes?: string[];
      examples?: string[];
      alternatives?: Array<{
        partOfSpeech?: string;
        explanation: string;
        reason?: string;
        confidence?: number;
      }>;
    };
    cefr?: {
      level: CefrLevel;
      source: "dictionary" | "ai";
      confidence?: number;
      reason?: string;
    };
    lexicalRelations?: {
      synonyms?: string[];
      antonyms?: string[];
      source: "dictionary" | "ai" | "mixed";
    };
    generator: {
      name: string;
      model?: string;
      version: string;
    };
  };
}
```

Validation rules:

- `lookupId` must exist and its normalized term must match `term`.
- `selectedMeaning` is required when the snapshot has an exact usable definition. `null` is allowed only for a source miss/fallback explanation.
- The server resolves indexes against the immutable snapshot and derives source headword, part of speech, definition, examples, and labels. The caller cannot overwrite source text.
- `description` is required and must be non-empty.
- Confidence values are finite numbers in `0..1`.
- `cefr.source: "dictionary"` is accepted only when the selected source definition contains that level; otherwise use `ai`.
- Arrays are normalized, deduplicated, and bounded. Proposed limits: 10 notes, 10 learner examples, 10 alternatives, and 20 synonyms/antonyms each.
- Upsert identity is owner + normalized term + normalized context + `lookupId` + generator name + generator version. Repeating the same call updates that cache entry instead of adding duplicates.
- Upsert does not implicitly call `vocabulary_save`.

#### Upsert output

```ts
{
  created: boolean;
  explanation: Explanation;
}
```

#### Delete input

```ts
{
  op: "delete";
  explanationId: string;
}
```

#### Delete output

```ts
{
  deleted: true;
  explanationId: string;
}
```

Delete is owner-scoped and returns `NOT_FOUND` for an unknown or inaccessible ID.

## 8. Cache behavior

### Dictionary cache

- Cache key: provider + normalized term + provider/dataset version + parser schema version.
- Dictionary snapshots are immutable and receive an opaque `lookupId`.
- One snapshot is marked active for a key; refresh creates and activates a new snapshot.
- Normal lookup returns the active unexpired snapshot.
- When the provider is unavailable, an expired snapshot may be returned with `cache.state: "stale_fallback"`. It must remain visibly stale.
- Provider `404` results are cacheable because the current bot already uses their suggestions and avoids repeated misses.

### Explanation cache

- Natural key: owner + normalized term + normalized context + active lookup snapshot + generator name + generator version.
- Context normalization trims and collapses whitespace but does not lowercase or semantically rewrite the sentence.
- A dictionary refresh or parser version change makes older explanations stale; it does not destroy them.
- A different generator name/version is a different cache partition, not an implicit match and not destructive invalidation.
- `explanation_get` ignores stale source snapshots by default.
- Cache storage preserves full context because a hash alone is not sufficient for listing or explaining why a sense was chosen.

## 9. Conceptual persistence model

This is a domain model, not an implementation migration.

```text
dictionary_snapshots
  id, provider, normalized_term, status, source_url,
  parser_version, dataset_version, data_json,
  fetched_at, expires_at, active

vocabulary_items
  id, owner_key, term, normalized_term,
  created_at, updated_at
  UNIQUE(owner_key, normalized_term)

explanations
  id, owner_key, term, normalized_term, context, normalized_context,
  lookup_id, selected_entry_index, selected_definition_index,
  learner_json, cefr_json, lexical_relations_json,
  generator_name, generator_model, generator_version,
  created_at, updated_at
```

Foreign keys connect explanations to immutable dictionary snapshots. Explanations do not require a vocabulary-item row, which keeps “cache this answer” separate from “add this to my vocabulary list.”

The existing Telegram `requests`, `request_messages`, `user_settings`, and `image_results` tables should not define this MCP schema. If data migration is later requested, successful existing requests and `ai_responses` can be transformed into `vocabulary_items` and `explanations` separately.

## 10. Expected MCP workflows

### Explain without saving

```text
explanation_get(
  term="bank",
  context="I sat on the bank of the river",
  generator={name: "chatgpt", version: "english-explanation-v1"}
)
  -> found=false

dictionary_lookup(term="bank")
  -> lookupId + source entries

[client model selects the river-edge definition and writes learner content]

explanation_write(op="upsert", ...)
  -> cached Explanation
```

### Save after explaining

```text
vocabulary_save(term="bank")
  -> created=true + VocabularyItem
```

### Reuse an explanation

```text
explanation_get(
  term="bank",
  context="I sat on the bank of the river",
  generator={name: "chatgpt", version: "english-explanation-v1"}
)
  -> found=true + full Explanation
```

No dictionary fetch or new AI generation is needed.

### Browse vocabulary efficiently

```text
vocabulary_list(cefr=["B1", "B2"], sort="recent", limit=50)
  -> compact VocabularyItem records

vocabulary_get(itemId="...")
  -> item + explanation summaries

explanation_get(explanationId="...")
  -> one full Explanation
```

## 11. Scope intentionally excluded from version 1

- Telegram command, message, callback, and deletion behavior;
- per-chat rendering settings and HTML/hashtag formatting;
- an internal OpenRouter client or model-selection settings;
- image search and binary image downloading;
- audio downloading or sending;
- approximate-sound `/like` generation;
- spaced repetition, review scheduling, quizzes, user-defined tags, and free-form notes attached directly to a vocabulary item;
- bulk clear/delete operations;
- admin cache inspection or cache eviction;
- migration of the existing Telegram SQLite database.

Pronunciation text/audio URLs, dictionary images, suggestions, idioms, and collocations remain available inside `dictionary_lookup`; they do not need dedicated tools until a concrete client workflow requires them.

## 12. Implementation technology

### Go

The server will be implemented in Go using the current stable Go toolchain and the official [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk).

Requirements:

- compile to one Linux executable named `english-learning-mcp`;
- expose the tools over MCP stdio so the executable remains independently testable;
- define tool inputs and outputs as typed Go structs and generate JSON Schema through the MCP SDK;
- pass `context.Context` through tool, dictionary, and storage boundaries for cancellation and deadlines;
- use standard `net/http` for dictionary requests;
- use a Go HTML parser behind a provider-neutral dictionary interface;
- use `log/slog` or an equivalent structured logger;
- write logs only to stderr because stdout is reserved for MCP JSON-RPC;
- shut down cleanly when stdin closes or the process receives termination.

Recommended package boundary:

```text
cmd/english-learning-mcp/  process entry point
internal/mcpserver/        MCP registration, schemas, and error mapping
internal/dictionary/       provider interface, HTTP client, parser, raw cache
internal/vocabulary/       saved vocabulary operations
internal/explanation/      explanation cache and validation
internal/storage/          SQLite queries and migrations
internal/domain/           provider-neutral domain structures
```

MCP handlers remain thin: validate input, call a domain service, and convert its result to the declared output. SQL, HTML parsing, and cache policy do not belong in tool handlers.

### SQLite

SQLite is the only application database. Use Go's `database/sql` with a maintained pure-Go SQLite driver so both the MCP server and tunnel client can be built with `CGO_ENABLED=0`.

Storage requirements:

- database path comes from `SQLITE_PATH`, defaulting to `/app/data/english-mcp.sqlite`;
- `/app/data` is a persistent Docker volume in production;
- `:memory:` is allowed only in automated tests;
- migrations are numbered SQL files embedded into the executable and applied transactionally at startup;
- startup fails on an incomplete or incompatible migration; it must not continue with a partially upgraded schema;
- enable foreign keys, WAL journal mode, a finite busy timeout, and normal synchronous mode;
- configure one open database connection initially to make pragma behavior and write ordering deterministic; increase only after measured need;
- mutations use explicit transactions when they touch more than one row or table;
- timestamps are stored as ISO 8601 UTC text;
- raw dictionary snapshots may be shared, while vocabulary items and explanations remain owner-scoped;
- malformed cached JSON is reported and treated as unusable, never silently returned;
- the process closes the database cleanly on shutdown.

The initial deployment is low-concurrency and I/O-bound. A server database is unnecessary. SQLite remains the correct choice until measured write contention or multi-replica deployment requires a different storage system.

## 13. Docker Compose and Secure MCP Tunnel

### Deployment topology

Use the same supervision pattern as `chatgpt-youtrack-mcp`:

```text
ChatGPT / OpenAI
        |
        | outbound Secure MCP Tunnel
        v
tunnel-client
        |
        | supervised MCP stdio child process
        v
english-learning-mcp
        |
        v
/app/data/english-mcp.sqlite
```

The container entry point is `tunnel-client run`. The tunnel reads `MCP_COMMAND`, launches `/usr/local/bin/english-learning-mcp`, and bridges its stdio MCP connection to the OpenAI control plane.

The first implementation should keep `tunnel-client` as a separate executable rather than embedding its Go SDK. This matches the working reference deployment, keeps the MCP server directly probeable, and allows tunnel upgrades without coupling tunnel lifecycle code to the application.

Properties:

- one Compose service and one container;
- no published ports, reverse proxy, domain, or inbound firewall rule;
- outbound TLS access to the dictionary provider and OpenAI control plane;
- a named volume for SQLite persistence;
- restart policy `unless-stopped`;
- both binaries run as an unprivileged user;
- tunnel health, readiness, metrics, and UI remain loopback-only on `127.0.0.1:8080` by default.

### Dockerfile pattern

The production Dockerfile uses two Go build stages and a minimal Debian runtime stage:

```dockerfile
# syntax=docker/dockerfile:1

ARG TUNNEL_CLIENT_VERSION

FROM golang:latest AS tunnel-builder
ARG TUNNEL_CLIENT_VERSION
WORKDIR /src/tunnel-client
RUN test -n \"${TUNNEL_CLIENT_VERSION}\" \
    && git clone --depth 1 --branch \"${TUNNEL_CLIENT_VERSION}\" \
       https://github.com/openai/tunnel-client.git .
RUN mkdir -p /out \
    && CGO_ENABLED=0 go build -trimpath -o /out/tunnel-client ./cmd/client

FROM golang:latest AS mcp-builder
WORKDIR /src/english-learning-mcp
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out \
    && CGO_ENABLED=0 go build -trimpath -o /out/english-learning-mcp \
       ./cmd/english-learning-mcp

FROM debian:stable-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 10001 app \
    && mkdir -p /app/data \
    && chown -R app:app /app

COPY --from=tunnel-builder /out/tunnel-client /usr/local/bin/tunnel-client
COPY --from=mcp-builder /out/english-learning-mcp /usr/local/bin/english-learning-mcp

USER app
ENTRYPOINT [\"/usr/local/bin/tunnel-client\", \"run\"]
```

`TUNNEL_CLIENT_VERSION` is pinned to `v0.0.13`, verified as the latest stable release when implemented. Any future pinned-version change must be checked against the authoritative release source.

### Docker Compose pattern

```yaml
services:
  english-learning-mcp:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        TUNNEL_CLIENT_VERSION: ${TUNNEL_CLIENT_VERSION:?set TUNNEL_CLIENT_VERSION}
    restart: unless-stopped
    environment:
      CONTROL_PLANE_API_KEY: ${CONTROL_PLANE_API_KEY}
      CONTROL_PLANE_TUNNEL_ID: ${CONTROL_PLANE_TUNNEL_ID}
      MCP_COMMAND: /usr/local/bin/english-learning-mcp
      SQLITE_PATH: /app/data/english-mcp.sqlite
      DICTIONARY_CACHE_TTL_DAYS: ${DICTIONARY_CACHE_TTL_DAYS:-30}
      LOG_LEVEL: ${LOG_LEVEL:-info}
      LOG_FORMAT: ${LOG_FORMAT:-json}
    volumes:
      - english-learning-data:/app/data

volumes:
  english-learning-data:
```

There is deliberately no `ports` section. The tunnel makes an outbound connection; the MCP server is not exposed as a public HTTP service.

### Environment pattern

Required runtime values:

```env
CONTROL_PLANE_API_KEY=sk-replace-me
CONTROL_PLANE_TUNNEL_ID=tunnel_replace_me
```

Required build value:

```env
# Replace with the latest stable tunnel-client tag verified from the official repository.
TUNNEL_CLIENT_VERSION=<verified-latest-stable-tag>
```

Optional application values:

```env
DICTIONARY_CACHE_TTL_DAYS=30
LOG_LEVEL=info
LOG_FORMAT=json
```

`CONTROL_PLANE_API_KEY` is a runtime tunnel key with Tunnels Read and Use permissions. It is not an OpenAI admin key. Secrets must remain outside the repository and must not be copied into image layers.

## 14. Operations and persistence

- The tunnel owns the MCP child-process lifecycle and propagates termination.
- `/healthz`, `/readyz`, `/metrics`, and `/ui` are served by the tunnel on loopback; they are inspected from inside the container unless an explicit trusted operator network is later approved.
- `/readyz` reports tunnel-client readiness. MCP tool discovery is verified separately through the tunnel status surface or a direct `tools/list` probe.
- A direct JSON-RPC `tools/list` probe against `/usr/local/bin/english-learning-mcp` verifies the binary independently of the tunnel.
- SQLite backups copy a consistent database snapshot, not only the main file while uncheckpointed WAL data exists.
- The named volume is retained across container rebuilds and replacements.
- Only one application replica may mount and write this SQLite volume. Horizontal replicas require a deliberate storage redesign.
- Application logs and tunnel logs are structured and must not contain dictionary HTML, private learner context, API keys, or complete MCP request payloads.

Deployment files:

```text
.dockerignore
.env.example
Dockerfile
docker-compose.yml
```

## 15. Implemented decisions

This implementation follows these decisions:

1. AI reasoning runs in the MCP client model rather than through OpenRouter inside the server.
2. Looking up or caching an explanation does not automatically save a term.
3. Explanations are context-specific and reference immutable dictionary snapshots.
4. Source facts and generated learner content remain separate.
5. Removing a vocabulary item does not silently remove reusable caches.
6. Version 1 consists of the eight tools listed above and excludes Telegram-only behavior.
7. The server uses Go, the official Go MCP SDK, and a persistent SQLite database.
8. Production uses Docker Compose with `tunnel-client` supervising the MCP binary over stdio and no public inbound port.
