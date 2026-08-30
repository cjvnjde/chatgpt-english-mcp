# English Learning MCP interface

## Purpose

The server has two responsibilities:

1. Look up English terms on Cambridge Dictionary and persist successful lookups.
2. Maintain an owner-scoped learning list of words and phrases.

The server does not generate or store AI explanations. A learning-list item may exist with no
dictionary lookup and no custom description. It may also contain a custom description from an
external source, a linked Cambridge lookup, or both.

In this document, **term** means a word, phrase, idiom, phrasal verb, or expression.

## Tools

| Tool | Purpose |
|---|---|
| `dictionary_lookup` | Return a permanent cached lookup, or explicitly refresh it. |
| `vocabulary_save` | Add or update one learning-list item. |
| `vocabulary_get` | Get one saved item with all available data. |
| `vocabulary_list` | Browse and search saved items. |
| `vocabulary_delete` | Remove one saved item. |

All input objects reject unknown fields. IDs are opaque. Timestamps are ISO 8601 UTC strings.
List limits default to 50 and must be between 1 and 100.

## Shared data

```ts
type CacheState = "hit" | "miss" | "refreshed" | "stale_fallback";

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
  customDescription?: string;
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
  customDescription?: string; // at most 5,000 Unicode characters
}
```

The operation is idempotent by normalized term:

- a new term is valid even when it has never been looked up;
- if a successful cached lookup exists, it is linked automatically;
- omitting `customDescription` preserves the existing value;
- supplying it adds or replaces the value;
- supplying an empty string clears the value;
- saving again after a lookup refresh updates the lookup link if needed.

Output:

```ts
{ created: boolean; item: VocabularyItem }
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
  sort?: "recent" | "oldest" | "alphabetical"; // default recent
  limit?: number;                                // default 50, maximum 100
  cursor?: string;
}
```

Output:

```ts
{ items: VocabularyItem[]; nextCursor?: string }
```

Every result embeds its optional complete lookup. `query` is a case-insensitive normalized-term
substring filter. Cursors are opaque and bound to the current filters and sort order.

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
- `UNAUTHORIZED` — the connection cannot access the owner-scoped record.
- `INTERNAL_ERROR` — unexpected persistence or serialization failure.

## Persistence migration

Migration 2 adds the optional lookup link and custom description to vocabulary items. Existing
items are linked to a current successful lookup when one exists. The obsolete explanations table
and its stored data are removed.
