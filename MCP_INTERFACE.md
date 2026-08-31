# English Learning MCP interface

## Purpose

The server has two responsibilities:

1. Look up English terms on Cambridge Dictionary and persist successful lookups.
2. Maintain an owner-scoped learning list of words and phrases.

The server does not generate or store AI explanations. A learning-list item may exist with no
dictionary lookup or learner metadata. It can independently contain a custom description with
source attribution, personal notes and examples, tags, a learning status, and a linked lookup.

In this document, **term** means a word, phrase, idiom, phrasal verb, or expression.

## Tools

| Tool | Purpose |
|---|---|
| `dictionary_lookup` | Return a permanent cached lookup, or explicitly refresh it. |
| `vocabulary_save` | Create or ensure one learning-list item without overwriting it. |
| `vocabulary_update` | Partially update an existing learning-list item. |
| `vocabulary_get` | Get one saved item with all available data. |
| `vocabulary_list` | Browse and search saved items. |
| `vocabulary_delete` | Remove one saved item. |

All input objects reject unknown fields. IDs are opaque. Timestamps are ISO 8601 UTC strings.
List limits default to 50 and must be between 1 and 100.

## Shared data

```ts
type CacheState = "hit" | "miss" | "refreshed" | "stale_fallback";
type LearningStatus = "new" | "learning" | "learned" | "archived";

type DictionaryLookup = {
  lookupId: string;
  requestedTerm: string;
  normalizedTerm: string;
  cache: {
    state: CacheState;
    fetchedAt: string;
  };
  source: {
    provider: string;
    sourceUrl?: string;
    datasetVersion?: string;
    parserVersion: number;
  };
  status: number;
  entries: DictionaryEntry[];
  suggestions: string[];
  images: DictionaryImage[];
  idioms?: string[];
  collocations?: Array<{ phrase: string; example?: string }>;
};

type VocabularyItem = {
  itemId: string;
  term: string;
  normalizedTerm: string;
  status: LearningStatus;
  tags: string[];
  customDescription?: string;
  descriptionSource?: {
    title?: string;
    url?: string;
  };
  notes: string[];
  examples: string[];
  lookup?: DictionaryLookup;
  createdAt: string;
  updatedAt: string;
};
```

`DictionaryEntry`, definitions, examples, pronunciations, audio, images, labels, related words,
synonyms, antonyms, idioms, and collocations retain the complete provider-backed lookup shape.

## `dictionary_lookup`

Input:

```ts
{
  term: string;
  refresh?: boolean; // default false
}
```

When a cached lookup contains entries and `refresh` is false, it is returned forever without an
upstream request. An explicit refresh fetches and persists a new immutable snapshot. If Cambridge
fails during refresh, the existing snapshot is returned with `cache.state = "stale_fallback"`.

Dictionary misses with an empty `entries` array are persisted as snapshots, but they are retried on
the next ordinary lookup so a temporary miss does not become permanent.

When a successful lookup is created or refreshed, any saved item for the same normalized term is
automatically linked to the new snapshot.

## `vocabulary_save`

Input:

```ts
{
  term: string;
  status?: LearningStatus; // default "new"
  tags?: string[];
  customDescription?: string; // at most 5,000 Unicode characters
  descriptionSource?: {
    title?: string; // at most 200 Unicode characters
    url?: string;   // absolute HTTP or HTTPS URL
  };
  notes?: string[];
  examples?: string[];
}
```

The operation is idempotent by normalized term:

- a new term is valid even when it has never been looked up;
- if a successful cached lookup exists, it is linked automatically;
- optional metadata initializes a new item;
- if the item already exists, none of its learner metadata is changed;
- a later successful dictionary lookup updates the lookup link automatically.

Output:

```ts
{ created: boolean; item: VocabularyItem }
```

Tags are trimmed, normalized to lowercase, deduplicated, and sorted. A description source requires
a non-empty custom description. Notes and examples preserve their supplied order.

## `vocabulary_update`

Input identifies exactly one existing item by `itemId` or `term`:

```ts
{
  itemId: string; // alternatively: term
  changes: {
    status?: LearningStatus;
    tags?: string[];
    customDescription?: string;
    descriptionSource?: { title?: string; url?: string };
    notes?: string[];
    examples?: string[];
  };
}
```

`changes` must contain at least one field. Omitted fields are preserved. Supplied arrays replace
the existing arrays, so `[]` clears them. An empty `customDescription` clears both the description
and its source. An empty `descriptionSource` object clears only the source.

Output:

```ts
{ item: VocabularyItem }
```

## `vocabulary_get`

Input is exactly one of:

```ts
{ itemId: string }
{ term: string }
```

Output:

```ts
{ item: VocabularyItem }
```

The optional `lookup` is embedded in full, so no follow-up dictionary request is necessary.

## `vocabulary_list`

Input:

```ts
{
  query?: string;
  statuses?: LearningStatus[];                // match any supplied status
  tags?: string[];                            // require all supplied tags
  hasLookup?: boolean;
  hasCustomDescription?: boolean;
  sort?: "recent" | "oldest" | "alphabetical"; // default recent
  limit?: number;                              // default 50, maximum 100
  cursor?: string;
}
```

Output:

```ts
{ items: VocabularyItem[]; nextCursor?: string }
```

Every result embeds its optional complete lookup. `query` is a case-insensitive normalized-term
substring filter. Multiple statuses match any value; multiple tags must all be present. Cursors
are opaque and bound to the current filters and sort order.

## `vocabulary_delete`

Input:

```ts
{ itemId: string }
```

Output:

```ts
{ deleted: true; itemId: string }
```

Deleting a learning-list item does not delete its permanent dictionary snapshots.

## Errors

- `INVALID_ARGUMENT` — malformed input, invalid cursor, or incompatible fields.
- `NOT_FOUND` — a requested saved item does not exist.
- `UPSTREAM_ERROR` — Cambridge failed and no usable cached snapshot exists.
- `UNAUTHORIZED` — direct external HTTP access did not supply the configured bearer token. The
  private tunnel listener does not require this token.
- `INTERNAL_ERROR` — unexpected persistence or serialization failure.

## Persistence migration

Migration 2 adds the optional lookup link and custom description to vocabulary items. Existing
items are linked to a current successful lookup when one exists. The obsolete explanations table
and its stored data are removed. Migration 3 adds learning status, tags, description attribution,
notes, and personal examples. Existing items start with status `new` and empty metadata arrays.
