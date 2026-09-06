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

A vocabulary item belongs to the namespace configured by `MCP_OWNER_KEY` and represents one learnable meaning. Multiple items may have the same normalized term. They share a cached dictionary lookup but have independent metadata, cards, and review histories. An item can exist without dictionary data and can contain:

- `new`, `learning`, `learned`, or `archived` status;
- normalized tags;
- a custom description with optional source attribution;
- ordered personal notes and examples;
- a selected dictionary definition and context;
- a link to the current dictionary snapshot, which still contains all definitions.

Saving is idempotent for the same normalized term and selected definition. A different selected definition creates a separate item. Intentional edits go through `vocabulary_update`. Archived items stay stored but are excluded from practice.

Learning status is learner-managed metadata. FSRS reviews do not automatically change `new` to `learning` or `learned`; all three active statuses remain eligible for selection. Only `archived` removes an item from the review queue.

### Learning state

Every active vocabulary item has one production-recall card. The card stores FSRS scheduling state separately from the learning content. Each accepted review creates an immutable attempt containing the rating, schedule before and after the review, and an optional comment.

Review tokens prevent duplicate attempts. Retrying the same token with the same rating and comment returns the original result with `duplicate: true`; reusing it with different data is rejected.

Each committed `learning_next` selection also appends an immutable presentation event, atomically with selecting and reading the item. It records the owner, vocabulary and card IDs, exercise mode, current `reviewToken`, server issuance time (`shownAt`), scheduled due time at issuance, and selection kind (`new`, `due`, or `early`). The response exposes the event's `presentationId` and its UTC `shownAt`.

A presentation means the server issued an item, not that a learner saw it or answered it. Delivery may fail after the event commits. Retrying `learning_next` records a fresh presentation and may choose another item; it does not retry the same presentation idempotently. Multiple presentations before a review share the card's pending `reviewToken`. An accepted review rotates that token, so the saved token links presentations to the eventual review attempt without implying a separate attempt for every presentation.

Presentation and review history survives vocabulary deletion. Recorded timestamps are retained as facts; upgrading does not invent presentation events or past presentation times from existing cards or reviews. Consequently, pre-upgrade exposure, actual human visibility, and recall duration cannot be inferred from this history alone.

## Expected workflows

### Look up and save a new term

1. Call `dictionary_lookup` with the term.
2. Present the useful dictionary data to the learner.
3. Choose the relevant definition from the lookup and call `vocabulary_save` with its exact `definition`, an optional short `context`, and any initial metadata.

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

- Use `vocabulary_get` by `itemId` for one exact saved meaning. Term lookup works only while that spelling has one saved meaning.
- Use `vocabulary_list` for search, status/tag filters, and cursor pagination.
- Use `vocabulary_update` to replace selected metadata fields or archive/reactivate an item.
- Use `vocabulary_delete` only when the item should be removed. Dictionary snapshots remain cached; learning cards are deleted with the item, while review attempts and presentation events remain as immutable history.

## How the next item is selected

`learning_next` always returns one active item when any exists. Selection varies the practice material without changing FSRS scheduling:

1. Consider all reviewed cards due now and all FSRS-new cards (not merely the oldest new items). Never select a future review while either eligible group exists. FSRS-new means unreviewed scheduling state, not the vocabulary item's learner-managed status.
2. Avoid cards in the owner's last three presentation events whose issuance times fall within the past 30 minutes, when another eligible card exists. If all eligible cards are recent, choose the least recently presented so small pools rotate. One active card can repeat; the cooldown is finite, not permanent exclusion.
3. When both due and new pools remain after the cooldown, choose the new pool with probability 20% and the due pool otherwise. This is an approximate 80/20 mix across many such selections, not a quota: availability and cooldown can change the observed mix.
4. Choose randomly within the selected pool using weights. Due cards receive an urgency multiplier of `1 + min(overdue time / max(scheduled interval, 1 day), 4)` and a failure multiplier of `1 + 0.5 × min(consecutive failures, 2) + 0.25 × min(lapses, 3)`. Both are bounded, so troublesome cards get extra weight without permanently dominating.
5. Apply a recency multiplier of `0.25 + 0.75 × clamp(time since last presentation / 24 hours, 0, 1)` to due and new cards; never-presented cards have multiplier 1. New cards use only this weight. The short cooldown expires after 30 minutes, while recency weight gradually recovers over 24 hours.

Only when no due or new cards exist does selection fall back to a future review: choose the nearest due time among nonrecent cards if possible, otherwise the least recently presented card. Failure counts do not move a later future review ahead of a nearer one.

The response describes the selected card with `reason`: `troublesome`, `failed`, `overdue`, `due`, `new`, or `early`. These descriptions are not a strict priority ordering; the presentation event's selection kind separately records whether the card was new, due, or early.

An item becomes `troublesome` after two consecutive `again` ratings or three total lapses. A tutor should then change the cue, contrast commonly confused terms, or introduce a mnemonic instead of repeating the same question.

## Content precedence during reviews

For compact review prompts, `learning_next` chooses content in this order:

- definition: custom description, then the first linked dictionary definition;
- example: first personal example, then the first linked dictionary example.

Set `includeComments: true` only when the tutor needs the full non-empty comment history. The latest comment is returned automatically.
