# How it works

English Learning MCP is the persistence and scheduling layer behind an AI English tutor. It gives an MCP host reliable tools and structured data; it does not generate lessons, definitions, or feedback by itself.

## What it can do

The server can:

- look up words, phrases, idioms, phrasal verbs, and expressions on Cambridge Dictionary;
- cache complete lookup snapshots, including definitions, examples, pronunciation, audio, images, related words, idioms, and collocations;
- save a personal vocabulary item independently of a dictionary lookup;
- attach custom descriptions and source attribution, notes, examples, tags, and a learning status;
- browse and filter saved vocabulary;
- choose exactly one item for production-recall practice;
- store immutable review attempts and optional notes about mistakes;
- use FSRS to schedule the next review;
- identify repeatedly failed or troublesome items.

The server cannot initiate a lesson or send a reminder. MCP is request/response: ChatGPT, another MCP host, or an external scheduler must decide when to call `learning_next`.

## The three data layers

### Dictionary snapshots

`dictionary_lookup` normalizes the requested term and checks SQLite first. A successful cached lookup is permanent for the current provider/parser version unless the caller explicitly requests a refresh.

On a cache miss, the server fetches and parses Cambridge Dictionary. A successful refresh creates a new immutable snapshot and makes it active. Saved vocabulary with the same normalized term is linked to the current successful snapshot automatically.

Empty lookup results are saved, but an ordinary future lookup retries them. If an explicit refresh fails and an older snapshot exists, the server returns that snapshot with `cache.state` set to `stale_fallback`.

### Vocabulary items

A vocabulary item belongs to the namespace configured by `MCP_OWNER_KEY` and is unique by normalized term within that namespace. It can exist without dictionary data and can contain:

- `new`, `learning`, `learned`, or `archived` status;
- normalized tags;
- a custom description with optional source attribution;
- ordered personal notes and examples;
- a link to the current dictionary snapshot.

Saving is idempotent. Calling `vocabulary_save` for an existing term returns it unchanged; intentional edits go through `vocabulary_update`. Archived items stay stored but are excluded from practice.

Learning status is learner-managed metadata. FSRS reviews do not automatically change `new` to `learning` or `learned`; all three active statuses remain eligible for selection. Only `archived` removes an item from the review queue.

### Learning state

Every active vocabulary item has one production-recall card. The card stores FSRS scheduling state separately from the learning content. Each accepted review creates an immutable attempt containing the rating, schedule before and after the review, and an optional comment.

Review tokens prevent duplicate attempts. Retrying the same token with the same rating and comment returns the original result with `duplicate: true`; reusing it with different data is rejected.

## Expected workflows

### Look up and save a new term

1. Call `dictionary_lookup` with the term.
2. Present the useful dictionary data to the learner.
3. Call `vocabulary_save` when the learner wants the term retained, optionally adding an initial status, tags, description, notes, or examples.

Lookup and saving are independent. A term may be saved first, and a later successful lookup will link itself automatically.

### Run one review

1. Call `learning_next` with `{}`.
2. Use its definition, example, and latest problem comment to create one production-recall question without revealing `term`.
3. Let the learner answer.
4. Rate the answer and call `learning_review` with the unchanged `reviewToken`.
5. Explain the answer and use the returned schedule only as learner-facing context when helpful.

Rating guidance:

| Rating | Use when |
|---|---|
| `again` | The answer is absent or incorrect. |
| `hard` | The answer is correct only after substantial effort or a strong hint. |
| `good` | The answer is correct with ordinary effort and no material hint. |
| `easy` | Recall is immediate and confident. |

The tutor should add a short review comment only when a concrete confusion, failed cue, or useful hint will improve the next attempt.

### Continue a lesson

After feedback, call `learning_next` again only when the learner wants another item. The server deliberately has no session length or daily quota.

### Manage vocabulary

- Use `vocabulary_get` for one exact saved term.
- Use `vocabulary_list` for search, status/tag filters, and cursor pagination.
- Use `vocabulary_update` to replace selected metadata fields or archive/reactivate an item.
- Use `vocabulary_delete` only when the item should be removed. Dictionary snapshots remain cached; learning cards are deleted with the item, while review attempts remain as immutable history.

## How the next item is selected

`learning_next` always returns one active item when any exists. The database orders candidates as follows:

1. due reviewed items with failures or at least three lapses;
2. other due reviewed items;
3. unseen items, oldest first;
4. the reviewed item with the nearest future due time.

Within the first groups, consecutive failures and lapses receive higher priority. The response explains the result with `reason`: `troublesome`, `failed`, `overdue`, `due`, `new`, or `early`.

An item becomes `troublesome` after two consecutive `again` ratings or three total lapses. A tutor should then change the cue, contrast commonly confused terms, or introduce a mnemonic instead of repeating the same question.

## Content precedence during reviews

For compact review prompts, `learning_next` chooses content in this order:

- definition: custom description, then the first linked dictionary definition;
- example: first personal example, then the first linked dictionary example.

Set `includeComments: true` only when the tutor needs the full non-empty comment history. The latest comment is returned automatically.
