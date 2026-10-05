# How it works

English Learning MCP is the persistence and scheduling layer behind an AI English tutor. It gives an MCP host reliable tools and structured data; it does not generate lessons, definitions, or feedback by itself.

This guide describes the implemented algorithm, not an idealized spaced-repetition system. The important distinction is **which item to ask now** versus **when that item should next be reviewed**. Those are separate decisions.

Numeric examples and formulas below use the default settings. Admin → **Settings** exposes validated, owner-specific controls for timing, FSRS retention/intervals/steps, mastery, selection, and reinforcement. Changes apply to subsequent operations without restarting; existing schedules and history are not rewritten. See [runtime algorithm settings](configuration.md#runtime-algorithm-settings) for the controls and limits.

Diagrams use Mermaid fenced blocks; formulas use LaTeX math. View this document in a Markdown renderer that supports both to see the rendered charts and equations.

## Reading map

- [System overview](#system-overview): the tutor, dictionary, vocabulary, selector, and scheduler.
- [Offline usefulness inference](#offline-usefulness-inference): evidence, hints, and classification.
- [How the next item is selected](#how-the-next-item-is-selected): eligibility, cooldown, exact weights, probabilities, and examples.
- [Learned-word reinforcement](#learned-word-reinforcement): comment-driven deep practice, personal interest, and the 25% word cap.
- [How a review changes the schedule](#how-a-review-changes-the-schedule): submitted grades, FSRS equations, and state transitions.
- [Content precedence during reviews](#content-precedence-during-reviews): how the selected item becomes a question.
- [What affects what](#what-affects-what): direct influences, indirect influences, and things the algorithm ignores.
- [Implementation map](#implementation-map): the source files behind the rules.

## Focused learning

The default `mixed` mode uses the full selection policy described below. Admin → Settings can enable `focused` mode and set `focusBatchSize` (default 10, range 1–100 saved meanings). `learning_focus` also inspects or starts/resumes/stops focus, and can explicitly choose the meaning IDs.

Focused mode creates a persistent batch, preferring already-learning meanings, then personal interest, usefulness, oldest creation time, and item ID. It keeps those members until all are learned or explicitly archived/deleted; it never replaces one learned member while others remain unfinished. The next `learning_next` then starts another batch. Changing the configured size affects the next batch. Switching to mixed mode retains membership for later resumption.

The shared selector applies its existing due-step priority, cooldown, and weights only to unfinished batch members. If all are scheduled in the future, it returns `waiting` and `nextDueAt` without issuing a card, token, or presentation. If no unfinished vocabulary exists, it returns `complete`. Learned maintenance remains a separate reinforcement activity while focused mode is enabled. Normal mastery thresholds and genuine-review scheduling still apply: staying in a batch does not earn mastery or force early repeats.

Admin likelihood previews use the same batch restriction. Before initial creation or after completion they preview the deterministically chosen next batch without saving it. Outside members have zero probability. Status/progress reflects corrections and demotions within the current batch; after rollover, a former member that becomes learning again waits for a future batch. The SQLite membership tables are included in full backups.

## System overview

There is no single “best word” score and no language model running inside the selector. The system combines a deterministic usefulness heuristic, a partly random selection policy, and an FSRS memory model.

```mermaid
flowchart TD
    Learner["Learner"] <--> Tutor["Connected AI tutor"]
    Tutor --> Lookup["dictionary_lookup"]
    Lookup <--> Cache["Cached Cambridge snapshots"]
    Lookup --> Cambridge["Cambridge on cache miss or refresh"]
    Tutor --> Save["vocabulary_save / vocabulary_update"]
    Evidence["Bundled ranks and expression evidence"] --> Estimate["Usefulness inference"]
    Save -->|"normalized term and optional hint"| Estimate
    Save --> Vocabulary["Saved meanings and metadata"]
    Estimate -->|"low / normal / high"| Vocabulary
    Vocabulary -->|"active production cards"| Select["learning_next selector"]
    History["Presentation history"] -->|"cooldown and recency"| Select
    Cards["FSRS card state"] -->|"new state, due date, failures"| Select
    Tutor --> Select
    Select -->|"append issuance event"| History
    Select -->|"one item, content, review token"| Tutor
    Tutor -->|"answer-quality rating and token"| Review["learning_review"]
    Review --> FSRS["FSRS scheduler"]
    FSRS --> Cards
```

The three numerical decisions have different jobs:

| Decision | Question answered | Output | When it runs |
|---|---|---|---|
| Usefulness inference | How broadly useful is this term in English? | `low`, `normal`, or `high` | Save, explicit usefulness update, or inference-revision backfill |
| Next-item selection | Which eligible saved meaning should we practice now? | One card, then one presentation event | Every `learning_next` call |
| FSRS scheduling | Given the review result, when should this card be due again? | Updated memory state and due date | Every accepted, nonduplicate `learning_review` |

The tutor chooses explanations, questions, hints, and answer-quality ratings. The server does not inspect the learner's answer text, infer personal interests from conversation, or autonomously start a session. Optional Anki sync is one-way content publication; Anki reviews do not feed this MCP's scheduler.

## What it can do

The server can:

- look up words, phrases, idioms, phrasal verbs, and expressions on Cambridge Dictionary;
- cache complete lookup snapshots, including definitions, examples, pronunciation, related words, idioms, and collocations, while copying supported source audio and images into SQLite;
- save a personal vocabulary item independently of a dictionary lookup;
- attach custom descriptions and source attribution, notes, examples, example images, tags, learning status, and general usefulness;
- browse and filter saved vocabulary;
- choose exactly one item for production-recall practice;
- store review attempts and optional notes about mistakes, with correction limited to the latest answer;
- use FSRS to schedule the next review;
- identify repeatedly failed or troublesome items.

The server cannot initiate a lesson or send a reminder. MCP is request/response: ChatGPT, another MCP host, or an external scheduler must decide when to call `learning_next`.

## The three data layers

### Dictionary snapshots

`dictionary_lookup` normalizes the requested term and checks SQLite first. A successful cached lookup is permanent for the current provider/parser version unless the caller explicitly requests a refresh.

Concurrent requests for the same normalized term and provider/dataset/parser version share one in-process fetch. Cancelling one caller does not cancel work needed by the others.

On a cache miss, the server fetches and parses Cambridge Dictionary. A successful refresh creates a new snapshot and makes it active. Its dictionary facts and ordering do not change afterward. Legacy/context-only vocabulary with the same normalized term follows the current successful snapshot automatically, including after parser upgrades within the same provider and dataset. Dictionary-selected meanings retain their original fact snapshot so definition indices, part of speech, and pronunciation remain consistent.

Genuine HTTP 404 results without entries are cached for five minutes; expiry or explicit refresh retries upstream. Transport and parser failures are not negative-cached. An HTTP 200 page without usable entries or suggestions is treated as an upstream failure rather than replacing good cached content. If a fetch fails and an older snapshot exists for the current parser, the server returns it with `cache.state` set to `stale_fallback`. Parsed resource links allow only HTTP(S) URLs without embedded credentials.

After a successful upstream fetch, the service makes a bounded, best-effort copy of every parsed audio and image resource from the exact configured Cambridge origin. Cross-origin URLs and redirects are rejected. Audio is limited to 5 MiB per file, images to 10 MiB, and one lookup to 64 files, 64 MiB, and 15 seconds of media work. Supported audio is MPEG, MP4, Ogg, or WAV; supported images are AVIF, GIF, JPEG, PNG, or WebP. Content is validated, keyed by SHA-256, and stored as a SQLite BLOB. The fetched snapshot keeps each original source URL and adds `mediaId` / `thumbnailMediaId` links to the local object. Matching source URLs in older snapshots receive the same local links, so existing dictionary-selected meanings gain stored media without changing their definitions or rebinding to a newer snapshot. If an asset refresh fails, an existing local link from the previous active snapshot for the same provider/dataset/parser is retained only for the same resource kind and exact original URL. Changed URLs never inherit old bytes. Without such a prior copy, failed, oversized, or unsupported assets remain unlinked without failing the dictionary lookup.

MCP tool responses remove dictionary `imageUrl` and `thumbnailUrl` fields from their text/structured JSON and deliver saved bytes as standard MCP `image` content blocks instead, including lookups nested in vocabulary and learning results. IDs, captions, and credits remain in the JSON, and each image block carries `_meta.mediaId`. Source URLs remain unchanged in SQLite and internal exports. Delivery is local-only, deduplicated by media ID, limited to four images and 8 MiB of decoded bytes (roughly 10.7 MiB after base64), with at most 16 media reads per call. A saved thumbnail is tried if the full image is unavailable or exceeds the remaining byte budget. Missing or excess images are skipped without failing the tool. Refresh a legacy lookup to download missing copies and backfill older snapshots. There is no public image-serving endpoint, no public-base-URL setting, and no change to the existing MCP authentication/tunnel boundary. Rendering requires a client that supports MCP image content; the Firefox sidebar currently reads only the text/structured metadata.

MP4 audio uses bounded container inspection rather than trusting the declared MIME type: Go's HTTP sniffer calls even AAC-only files `video/mp4`. Valid audio-only track handlers are accepted; video/mixed tracks and malformed or truncated boxes are rejected. No external media decoder is needed at runtime.

### Vocabulary items

A vocabulary item belongs to the namespace configured by `MCP_OWNER_KEY` and represents one learnable meaning. Multiple items may have the same normalized term and can share a cached dictionary lookup, but have independent metadata, cards, and review histories. An item can exist without dictionary data and can contain:

- `new`, `learning`, `learned`, or `archived` status;
- `low`, `normal`, or `high` usefulness;
- independent `low`, `normal`, or `high` personal interest;
- normalized tags;
- a custom description with optional source attribution;
- ordered personal notes and examples;
- up to 12 attached JPEG, PNG, GIF, WebP, or AVIF example images, each with an optional filename and learner-facing example or memory cue;
- a saved context, whether or not a dictionary sense was selected;
- an optional selected dictionary definition and its original lookup fact snapshot, which contains all definitions.

Saving is idempotent for the same normalized term and selected definition. A different selected definition creates a separate item. Intentional edits go through `vocabulary_update`. Archived items stay stored but are excluded from practice.

Example images are added or removed through the bearer-authenticated admin editor after the vocabulary item exists. They belong to that exact owner/item identity, advance its edit revision, and are deleted with the item. The binary data lives in the same SQLite database as the vocabulary record.

Learning status is automatically maintained from accepted feedback, while explicit manual overrides remain available. New vocabulary becomes `learning` after its first graded attempt. Learned vocabulary remains reviewable at lower priority, and a failed recall returns it to learning. `archived` stays manual and excluded; merely presenting or skipping a word never advances mastery.

Usefulness estimates general English value, independently of personal interests, recall difficulty, and learning status. It combines bundled word-frequency and expression evidence with an optional API hint. Changing usefulness changes new-card selection weight, not due-review weight or FSRS review dates. Different saved senses share term-level evidence but can have different hints.

Personal interest records explicit learner priority: high for interesting/learn sooner, low for less important, normal to reset. It multiplies all weighted new/due selection pools and learned-word reinforcement, but never excludes low-interest items or changes FSRS. Existing items default to normal.

### Automatic status and daily mastery

Each saved meaning has `masteryStreak` and the UTC date/time of its last credited flawless recall. A session is a **UTC calendar day**, not a chat conversation or rolling 24-hour window:

- `good` or `easy`: credit at most one recall on a day later than the last credited UTC day.
- `hard` or `again`: reset the streak on **every** accepted attempt. Keep the last credited day, so good → again → good in one day ends at zero, not one.
- Same-day successes and identical retries never add credits. Every genuine accepted attempt still updates FSRS; the daily limit applies to mastery credit, not review logging or failure processing.
- Promote to `learned` only after **at least five qualifying recalls on separate days**, a `good`/`easy` answer, FSRS Review state, and a scheduled interval of **at least 21 days**.
- A learned word stays learned after successful but effortful `hard` recall; `again` demotes immediately. Reinforcement `again` also resets the streak and brings the normal review due now. Reinforcement success does not manufacture scheduled-recall credits.
- Correcting the latest review replays its original status and daily evidence; it cannot count the same answer twice. Newer reinforcement feedback for that meaning blocks changed corrections.

Days without practice do not reset the streak; FSRS governs when another review is due. UTC midnight begins a new session even if less than 24 hours has elapsed. A backward clock change cannot add credit for an older day. Daily credit and status are persisted, so restarting the server does not reset them.

Five days is the learner's conservative promotion policy, not a scientifically universal mastery threshold. The [Anki manual](https://docs.ankiweb.net/stats.html#true-retention-table) uses ≥21 days as a mature-card convention and counts the first review per day in its retention statistic. Its [FSRS guidance](https://docs.ankiweb.net/deck-options.html#fsrs) recommends a 90% recall-retention starting point and warns that repeated same-day practice contributes less to long-term retention. This app retains FSRS's 90% target; that probability is **not** the fraction of learned words in a session. The 10%–40% maintenance mix below is an application workload heuristic, not a research-established optimum or a trained reinforcement-learning policy.

Upgrades preserve existing manual statuses and initialize unproven mastery at zero rather than invent historical successes. Manual learned status remains an explicit override, not evidence of five qualifying recalls. A real status change updates vocabulary timestamps and invalidates stale admin drafts via the existing edit-revision trigger.

### Offline usefulness inference

The application embeds English ranks from [wordfreq](https://github.com/rspeer/wordfreq) and [FrequencyWords](https://github.com/hermitdave/FrequencyWords), plus expression data from Wiktionary/Kaikki, MAGPIE, and WordNet. Inference requires no external API, runtime download, Python process, or Cambridge lookup. These sources are historical and partly overlap; the combination is a transparent heuristic, not independent statistical evidence or a guarantee that a word is worth learning.

The two word-frequency sources vote only on exact normalized full-term matches:

| Rank in that source | Vote |
| --- | --- |
| 1–1,000 | `high` (+1) |
| 1,001–5,000 | `normal` (0) |
| Above 5,000 | `low` (−1) |
| Missing | Abstain |

Each matched source has weight 1. An explicitly supplied `usefulness` hint has weight 2, using the same −1/0/+1 values. A weighted mean strictly above ⅓ produces `high`; strictly below −⅓ produces `low`; the inclusive middle produces `normal`. With no evidence or hint, the result is `normal`.

For example, two `high` source votes and a `low` API hint produce `normal`, not a forced override in either direction. Two `high` votes and a `normal` hint still produce `high`. Omitting the hint is different from submitting `normal`: omission contributes no vote.

The word-rank path remains exact; it never estimates an idiom's frequency by combining individual word frequencies. Expression evidence is evaluated separately:

| Expression evidence | Vote |
| --- | --- |
| Wiktionary marks every retained sense for the normalized headword rare, archaic, obsolete, or dated | `low`, weight 1 |
| MAGPIE has confident idiomatic uses in at least 10 distinct documents | `high`, weight 1 |
| WordNet records at least 5 tagged occurrences | `high`, weight 1 |
| Mere dictionary presence, missing counts, or insufficient attestations | Abstain |

Unlabelled or unrestricted Wiktionary senses prevent a blanket restricted-usage judgement; the learner's chosen sense does not narrow that source evidence. MAGPIE counts only confident idiomatic uses (annotation confidence at least 0.75); literal, unclear, and false-extraction instances do not add idiomatic evidence. Its BNC candidates were capped at 200 per idiom type, with additional PMB examples not downsampled. These counts establish repeated attestation, not a population frequency ranking. WordNet tags likewise come from a limited annotated corpus. Small counts never imply that an expression is useless. The same weighted-mean rule combines these votes with the original API hint.

Wiktionary's generic `common` tag is not used as positive evidence: it can describe common law or a regional preference rather than overall frequency.

#### Exact usefulness formula

Let each voting source $j$ have value $v_j\in\{-1,0,+1\}$ and presence indicator $a_j\in\{0,1\}$. There are up to five source votes: two word-rank sources and the three expression sources above. Let $b=1$ when an API hint was supplied, otherwise $b=0$, and encode that hint as $h=-1,0,+1$.

$$
Q=\sum_j a_jv_j+2bh
\qquad
B=\sum_j a_j+2b
$$

$$
\operatorname{usefulness}=
\begin{cases}
\text{high} & 3Q>B\\
\text{low} & 3Q<-B\\
\text{normal} & \text{otherwise}
\end{cases}
$$

For $B>0$, this is the weighted mean $Q/B$ with strict cutoffs at $-1/3$ and $+1/3$. The code uses integer comparisons so the boundaries are exact. With $B=0$, both comparisons are false and the result is `normal`, without dividing by zero.

A `normal` vote is **not abstention**: it adds weight to the denominator but zero to the numerator. Expression counts are thresholded, not proportional: 10 qualifying MAGPIE documents and 1,000 qualifying documents each contribute just one high vote. Word ranks inside a band are likewise equal; rank 1 receives no stronger vote than rank 1,000.

| Evidence and hint | $Q$ | $B$ | Mean | Result |
|---|---|---|---|---|
| No evidence, no hint | 0 | 0 | Not computed | `normal` |
| No evidence, `high` hint | 2 | 2 | 1 | `high` |
| One high source, no hint | 1 | 1 | 1 | `high` |
| One high source, `normal` hint | 1 | 3 | $1/3$ | `normal` |
| One low source, `normal` hint | −1 | 3 | $-1/3$ | `normal` |
| Two high sources, `normal` hint | 2 | 4 | $1/2$ | `high` |
| Two high sources, `low` hint | 0 | 4 | 0 | `normal` |
| Restricted Wiktionary + qualifying MAGPIE + qualifying WordNet, no hint | 1 | 3 | $1/3$ | `normal` |

These are arithmetic examples, not claims that a particular spelling has those source ranks. A double-weighted hint is influential but is not an override.

#### How a term matches the evidence

The saved display spelling has whitespace trimmed/collapsed. The inference key additionally lowercases and converts curly single/double quotes to straight quotes. It does not strip other punctuation, normalize hyphens, remove words, or stem the term.

```mermaid
flowchart TD
    Term["Normalized saved term"] --> Ranks["Exact full-term rank lookup in both corpora"]
    Term --> Canonical{"Exact canonical multiword entry?"}
    Canonical -->|"Yes"| Expression["Use only that target's expression votes"]
    Canonical -->|"No"| Variants["Collect aliases, source-backed inflections, and matching templates"]
    Variants --> Count{"Number of distinct targets?"}
    Count -->|"One"| Expression
    Count -->|"More than one"| Abstain["Expression evidence abstains"]
    Count -->|"Zero"| Typo["Try constrained one-token, one-edit literal matches"]
    Typo --> Unique{"Exactly one target?"}
    Unique -->|"Yes"| Expression
    Unique -->|"No"| Abstain
    Ranks --> Combine["Combine source votes and optional double-weighted hint"]
    Expression --> Combine
    Abstain --> Combine
    Combine --> Category["Persist low / normal / high"]
```

Matching follows these tiers, stopping at the first tier with a match:

1. **Exact canonical expression.** This takes precedence even if the entry contributes no votes.
2. **Aliases, first-verb inflections, and explicit templates together.** They are one competing tier, not an alias-first priority list. All matching paths must resolve to the same canonical target; an alias conflicting with a template makes expression evidence abstain.
3. **Constrained typo fallback.** Only when the preceding tier found no match, consider complete literal multiword surfaces with exactly one insertion, deletion, substitution, or adjacent transposition in one token. Both changed tokens must have at least four Unicode characters, and every other token and its order must match. If the changed input token ranks in the top 50,000 of either corpus, **all four edit types** are blocked. Multiple possible canonical targets abstain.

Ambiguous or unscored matches do not fall through to a more useful target. Finding the same target through several aliases does not multiply its votes.

First-verb variants require source evidence that the expression is verbal and an actual WordNet verb exception form for its first token. There is no general runtime inflection generator. Other variants must come from explicit source aliases/observations.

Templates have narrow, source-declared slots:

| Token in a source template | Accepted single-token replacements |
|---|---|
| `one's`, `someone's`, `somebody's` | `my`, `your`, `his`, `her`, `its`, `our`, `their`, `one's`, `someone's`, `somebody's` |
| `someone`, `somebody` | `me`, `you`, `him`, `her`, `it`, `us`, `them`, `someone`, `somebody` |

For example, a source template `keep one's chin up` can match `keep my chin up`; it does not make arbitrary names or extra words acceptable. `something` is not a wildcard. Template instantiation is not generally combined with typo correction; an independently attested literal surface can still be a typo target.

There is no arbitrary token removal, reordering, substring lookup, or semantic similarity. Matching affects inference only: it never corrects the saved spelling, merges saved meanings, or changes `vocabulary_get`/`vocabulary_list` lookup rules. Unknown or ambiguous expression evidence leaves the **independent exact full-term rank votes** intact. Only if those also abstain does the hint alone determine the result, or `normal` if omitted. A corrected expression target does not replace the saved input for word-rank lookup.

Cambridge definition `labels` can contain CEFR levels and usage information already captured by the dictionary parser. [CEFR labels](https://dictionary.cambridge.org/us/help/) describe learner proficiency, not a direct usefulness rating: advanced vocabulary is not automatically unnecessary. Offline inference does not fetch Cambridge or depend on its availability; the tutor may consider existing definition context when supplying a hint.

The commercial Linguatools collocation package is not bundled. It requires an actual licensed download; permission to use it privately does not provide the data.

The runtime consumes bundled standard-competition ranks, not live usage counts or wordfreq Zipf scores. Terms in the same wordfreq frequency bin or with the same FrequencyWords count share the first rank of that tied group; later groups count every original row. Normalization collisions retain the best group rank. Equal-frequency terms therefore cannot cross a usefulness boundary merely because of row ordering. Source overlap, historical coverage, and term-level aggregation still limit what “usefulness” can mean: this is not a calibrated probability, personal relevance model, or score for the learner's exact selected sense.

#### Why saved hints and calculated results stay separate

The original API hint is stored separately from the effective result. Duplicate saves preserve both; a usefulness update replaces the hint and recalculates the result; unrelated updates preserve both. Migration `009` retains every previous usefulness value as a hint and recalculates existing vocabulary across all owners and statuses. Future dataset or scoring-policy revisions recalculate from those original hints, never from the previous calculated result. Revision-matched restarts do not rescan vocabulary. Backfills preserve existing timestamps, cards, review tokens, presentations, and review history.

| Operation | Original hint | Effective usefulness |
|---|---|---|
| Save with no hint | Absent (`NULL`) | Infer from sources alone |
| Save with a hint | Store the supplied category | Infer from sources plus hint |
| Save the same meaning again | Preserve | Preserve, even if the new request supplies a different hint |
| Update with `changes.usefulness` | Replace | Recalculate |
| Update other metadata only | Preserve | Preserve |
| Restart with the same inference revision | Preserve | Preserve; no vocabulary rescan |
| New dataset/scoring revision | Preserve | Recalculate for every owner and status |

The public `usefulness` field exposes the effective result, not the original hint. There is no API operation to clear an existing hint back to absence: omission preserves it and submitting `normal` adds a real weight-2 vote. Do not feed a returned result back as a new hint unless that is an intentional new assessment.

### Learning state

Every active vocabulary item has one production-recall card. The card stores FSRS scheduling state separately from the learning content. Each accepted review creates an attempt containing the submitted rating, effective scheduling rating, schedule before and after the review, and an optional comment. Its identity, review time, and pre-review state are immutable; only the owner's latest accepted attempt can have its grade, comment, and resulting schedule corrected.

Review tokens prevent duplicate attempts. Retrying the same token with the saved rating and comment returns the saved result with `duplicate: true`; reusing it with different data is rejected. Explicit corrections use `learning_review_update` and replace those saved values.

Each committed `learning_next` selection also appends an immutable presentation event, atomically with selecting and reading the item. It records the owner, vocabulary and card IDs, exercise mode, current `reviewToken`, server issuance time (`shownAt`), scheduled due time at issuance, and selection kind (`new`, `due`, or `early`). The response exposes the event's `presentationId`, its UTC `shownAt`, and the selected vocabulary `itemId` for exact follow-up reads or edits.

`learning_next` also returns available tutoring context without a follow-up `vocabulary_get`: `customDescription`, `descriptionSource`, `notes`, all personal `examples`, `tags`, and `sense` (the selected dictionary definition and its examples, part of speech, and pronunciations). Empty optional fields are omitted. The existing compact `definition` and `example` retain their fallback behavior; `sense` preserves the dictionary meaning separately when a custom description overrides `definition`. The full dictionary lookup and unrelated meanings are not included. These fields do not depend on `includeComments`, which only adds review-comment history; the latest comment is always returned when available.

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
2. If `reason` is `early`, end normal scheduled practice without asking a question or recording a review, unless the learner explicitly wants optional early practice. Otherwise use its definition, example, and latest problem comment to create one production-recall question without revealing `term`. If the returned context does not identify the meaning, clarify it rather than guessing a dictionary sense.
3. Let the learner answer.
4. Rate the answer and call `learning_review` with the unchanged `reviewToken`.
5. Explain the answer and use the returned schedule only as learner-facing context when helpful.

If the learner corrects the grading of that answer, call `learning_review_update` with the same original token and the corrected rating, such as `again`. Do not submit a second review. Omit `comment` to retain the saved note, or replace/clear it explicitly. Only the latest accepted repetition across all vocabulary items is editable; fetching another item is allowed, but accepting another answer locks the earlier one.

Rating guidance:

| Rating | Use when |
|---|---|
| `again` | The answer is absent or incorrect. |
| `hard` | The answer is correct only after substantial effort or a strong hint. |
| `good` | The answer is correct with ordinary effort and no material hint. |
| `easy` | Recall is clearly effortless and confident. |

The tutor should add a short review comment only when a concrete confusion, failed cue, or useful hint will improve the next attempt.

The server applies one one-way timing rule: a submitted `good` becomes `easy` if the accepted review is strictly within the configured fast-answer window (default 30 seconds) after the token's first presentation. Server issuance includes model processing and delivery; this is a quick-response heuristic, not a measurement of pure human recall time.

Slow answers are never penalized and other grades never change. `learning_review` returns the final `rating`; the same grade is stored and used by FSRS. Retries preserve the saved grade and schedule. Retry with the original request rating and comment, even when `good` was promoted to `easy`; use corrected values after an explicit correction changes the attempt.

### Continue a lesson

After feedback, call `learning_next` again only when the learner wants another item. End normal practice on `reason: "early"` unless the learner explicitly opts into early practice, as described in the [reusable tutor prompts](prompts.md). The server deliberately has no session length or daily quota and still issues the early presentation; this stopping rule belongs to the tutor, not an API mode.

### Manage vocabulary

- Use `vocabulary_get` by `itemId` for one exact saved meaning. Term lookup works only while that spelling has one saved meaning.
- Use `vocabulary_list` for search, status/tag/type/part-of-speech/usefulness/interest filters, and cursor pagination.
- Use `vocabulary_update` to replace selected metadata fields or archive/reactivate an item.
- Use `vocabulary_delete` only when the item should be removed. Dictionary snapshots remain cached; learning cards are deleted with the item, while review attempts and presentation events remain as immutable history.

### Build sentences without tracking a review

For a request such as “give me five words I'm learning or have learned and ask me to make sentences,” call `vocabulary_list` with `statuses: ["learning", "learned"]`, `sort: "random"`, and `limit: 5`. Use `limit: 1` for one item. The AI uses each returned saved meaning to create the exercise and discusses the learner's answers in chat; it may show the target words.

This is distinct from both scheduled review and tracked learned-word reinforcement: no token, presentation event, review submission, schedule update, difficulty update, or cooldown. It works with a small pool and leaves pending reviews untouched. Selection is random over matching saved meanings, without learning-priority weights. Repeated calls may repeat items; send earlier `itemId` values in `excludeItemIds` to avoid repeats. An empty match returns an empty list.

Optional filters include exact stored usefulness/personal interest, lexical single-word versus multiword expression, and dictionary part of speech for the selected sense. Missing or unresolved part-of-speech metadata does not match that filter. Omitted statuses include archived items, so request the intended statuses explicitly. See the [full filter contract](tools.md#vocabulary_list) and [copyable prompt](prompts.md#free-form-vocabulary-exercises).

## How the next item is selected

`learning_next` returns one active item when any exists, including an early review when no FSRS-new or due cards exist. It is not a “get only today's due cards” endpoint. If there are no active cards, it returns `NOT_FOUND`.

### Decision flow

```mermaid
flowchart TD
    Start["learning_next"] --> Load["Load all owner's non-archived production cards"]
    Load --> Any{"Any active cards?"}
    Any -->|"No"| Empty["NOT_FOUND"]
    Any -->|"Yes"| Eligible{"Any FSRS-new or due cards?"}
    Eligible -->|"Yes"| Filter["Apply global cooldown across all eligible cards"]
    Filter --> Adapt["Relax oldest exclusions when needed for variety"]
    Adapt --> Steps{"Any selectable nonlearned due Learning/Relearning cards?"}
    Steps -->|"Yes"| Learning["Choose the learning/relearning pool"]
    Steps -->|"No"| Both{"Learned and active groups both remain?"}
    Both -->|"Yes"| Mix["Learned 10%–40% by weighted backlog; split active remainder by exposure"]
    Both -->|"No"| Only["Use the nonempty group; split active new/review by exposure if needed"]
    Learning --> Lottery["Weighted random draw within that pool"]
    Mix --> Lottery
    Only --> Lottery
    Eligible -->|"No"| Future{"Any nonrecent future cards?"}
    Future -->|"Yes"| Restrict["Use only nonrecent future cards"]
    Future -->|"No"| All["Use all future cards"]
    Restrict --> Oldest["Nonlearned first, then least recent event ID, due time, card ID"]
    All --> Oldest
    Commit["Read chosen content and commit a presentation event"]
    Lottery --> Commit
    Oldest --> Commit
    Commit --> Return["Return item, reason, presentation ID, time, and review token"]
```

The ordering matters: **eligibility → global adaptive cooldown → learning-step priority or adaptive pool choice → within-pool weight**. A large weight cannot bypass an earlier stage.

### 1. Build the candidate pools

The selector loads all saved production cards for `MCP_OWNER_KEY` whose vocabulary status is not `archived`. It does not use a page from `vocabulary_list`, an oldest-new shortlist, or a tag filter.

For current server time $t$:

| Pool | Exact condition |
|---|---|
| New | Status is not `learned`, `fsrs_state == 0`, regardless of the stored due date |
| Due learning/relearning | Status is not `learned`, FSRS Learning (1) or Relearning (3), and `due_at <= t` |
| Due nonlearned review | Status is not `learned`, FSRS Review (2), and `due_at <= t` |
| Learned maintenance | Status is `learned`, and the card is FSRS-new or due |
| Future | `fsrs_state != 0` and `due_at > t` |

Vocabulary status chooses learned versus active priority; FSRS state determines timing and the remaining active pools. Manually learned FSRS-new cards belong to the learned pool, not the new-introduction pool. No learned future card is inserted while new/due work exists.

The unit of selection is a **saved meaning/card**, not a spelling. Two meanings of the same term have separate histories and can both be selected. There is no word-level deduplication across their cards.

### 2. Apply the short cooldown

A card is *recent* only when both conditions hold:

1. Its latest presentation is among the owner's last **three presentation events**, ordered by event ID for production practice.
2. The elapsed time since that presentation is **less than 30 minutes**.

This is not “all words shown in the last 30 minutes,” and the three events need not represent three distinct cards. Once an event falls outside the last three, its card can compete again immediately. New and mature-review cards still receive the softer recency penalty below; learning/relearning steps do not. At exactly 30 minutes the hard cooldown has expired.

Start by removing recent cards globally from all eligible pools, before testing learning-step priority. Relax this exclusion only when it would prevent useful variety:

- If two or more candidates remain, keep the exclusion unchanged.
- If exactly one remains and it has never been presented or was last presented at least 30 minutes ago, keep that fresh alternative alone.
- If exactly one remains but it was shown within 30 minutes, also admit the least recently presented excluded eligible card, unless that card is the most recently presented active card.
- If none remain, admit the oldest eligible card and, unless it is the most recently presented active card, the second-oldest eligible card.
- Never admit a future card through this relaxation. Future cards' presentation IDs still identify the latest presentation, so two due cards can both compete when the last-shown card is not due yet.

When multiple surviving candidates belong to the chosen pool, this permits a weighted choice between older cards instead of forcing a fixed rotation. Pool priority can still leave only one selectable learning step, so a predictable sequence remains possible. With only two active cards, avoiding immediate repeats means alternation. With only one eligible card, it may repeat immediately; future cards do not become due just to add variety. Random draws can also happen to repeat an order.

“Oldest” compares each card's latest **event ID**, not its timestamp; ties use ascending card ID. This preserves issuance order even when events share a timestamp. Archived/deleted items' retained events can still occupy positions in the owner's last-three-event window; the latest active presentation is the greatest event ID among the loaded cards.

The implementation treats a negative elapsed interval after a backward clock change as recent when its ID is in the window. The recency weight clamps that interval to zero. A backward-clock interval never earns a fast-answer promotion.

### 3. Prioritize learning steps, then reserve learned maintenance

After cooldown and relaxation, any selectable **nonlearned** due Learning/Relearning steps receive the entire draw. If all such steps remain excluded, other cards supply spacing; a future learning step never blocks eligible work.

Otherwise let $W_K$ be the total selectable learned-card weight and $W_A$ the total active new/nonlearned-review weight. When both groups exist:

$$
S_K=\operatorname{clamp}\left(\frac{W_K}{9W_A+W_K},0.10,0.40\right)
$$

The learned group receives $S_K$ and the active group $1-S_K$. Equal weighted workloads yield 10% learned; a 3:1 learned-to-active workload yields 25%; a large learned backlog caps at 40%, preserving a majority for acquisition. A sole learned or active group receives 100%. These weights include bounded urgency, failures, exposure, and interest, so neglected learned material can gain share without dominating active work.

Within the active remainder, let $E_N=\sum_{i\in N}\rho_i$ for FSRS-new cards and $E_R=\sum_{i\in R}\rho_i$ for nonlearned Review-state cards. If both exist, new receives $\operatorname{clamp}(E_N/(E_N+E_R),0.2,0.8)$ of that remainder and review receives the rest; a sole active pool receives all of it. This inner exposure split is independent of usefulness, interest, and failures.

For example, without learned candidates or selectable steps, 85 equally unpresented new cards and 15 nonlearned reviews allocate 80% to new. Adding an equal weighted learned workload reserves 10% for learned, leaving 72% new and 18% nonlearned review. These are conditional next-call probabilities, not guaranteed session quotas. Due learned words retain positive probability outside step precedence and cooldown, but no fixed number of random calls guarantees any particular word.

### 4. Calculate each card's weight

Define `clamp(x, a, b) = min(max(x, a), b)`. All elapsed durations in these selection formulas are real elapsed hours/days, not calendar-day boundaries.

**Usefulness multiplier $U_i$:**

$$
U_i =
\begin{cases}
0.5 & \text{low}\\
1 & \text{normal}\\
2 & \text{high}
\end{cases}
$$

These categories are the **calculated, persisted results** of usefulness inference, not necessarily the tutor's submitted hints. They weight only FSRS-new cards, never due learning steps or mature reviews.

**Personal-interest multiplier $I_i$:** low = 0.5, normal = 1, high = 2. Unlike general usefulness, it multiplies all four weighted pools; it cannot bypass cooldown or pool priority.

**Presentation exposure multiplier $\rho_i$:**

$$
\rho_i =
\begin{cases}
4 & \text{never presented}\\
0.25 + 0.75\,\operatorname{clamp}\left(
\frac{t-\text{lastShownAt}_i}{24\text{ hours}},0,1\right)
& \text{presented at most 24 hours ago}\\
1 + \operatorname{clamp}\left(
\frac{t-\text{lastShownAt}_i-24\text{ hours}}{29\text{ days}},0,1\right)
& \text{presented more than 24 hours ago}
\end{cases}
$$

| Time since last presentation | Exposure multiplier |
|---|---|
| Just now | 0.25 |
| 30 minutes | 0.265625 |
| 1 hour | 0.28125 |
| 6 hours | 0.4375 |
| 12 hours | 0.625 |
| 24 hours | 1 |
| 30 days or more | 2 |
| Never presented | 4 |

The adaptive cooldown and this multiplier are separate mechanisms. A new or mature-review card leaving cooldown after 30 minutes has recovered eligibility, not full weight. Exposure affects both its pool's adaptive share and its weight within that pool. Never-presented cards receive twice the maximum presented-card exposure so saved material is unlikely to remain hidden behind repeatedly trained words. Learning/relearning cards have no soft exposure multiplier, but still obey the hard cooldown.

**Due urgency multiplier $A_i$:**

$$
H_i =
\begin{cases}
1/6 & \text{nonlearned Learning or Relearning}\\
\max(24\,\text{scheduledDays}_i,24) & \text{Review or learned maintenance}
\end{cases}
\qquad
A_i = 1 + \min\left(\frac{\max(\text{overdueHours}_i,0)}{H_i},4\right)
$$

Here $H_i$ is measured in hours. An exactly due card has urgency 1. For mature reviews, one full interval overdue gives 2; four or more intervals overdue gives the maximum 5, with a minimum interval of one day. A seven-day card one day overdue gets $1+1/7$, not 2. Learning/relearning uses a fixed **10-minute** denominator: 10 minutes overdue gives 2, and 40 minutes or more overdue gives 5, regardless of `scheduledDays`. This is a selector weight, not a change to FSRS's actual learning-step schedule.

**Failure multiplier $F_i$:**

$$
F_i = 1
+ 0.5\,\min(\text{consecutiveFailures}_i,2)
+ 0.25\,\min(\text{lapses}_i,3)
$$

For example, one consecutive failure and one lapse give 1.75. Two consecutive failures and three lapses give the maximum 2.75. Additional failures beyond these caps can still affect FSRS memory state; they add no further selection multiplier. A lapse is specifically a failed review from FSRS `Review` state, not every `again` answer.

**Final lottery weights:**

$$
W_i^{\text{new}} = \rho_i U_i I_i
\qquad
W_i^{\text{review}} = A_i F_i \rho_i I_i
\qquad
W_i^{\text{learning}} = A_i F_i I_i
$$

Learned FSRS-new cards use the new-card weight but enter the learned pool. Other learned cards use the review weight and day-scale urgency, even when their manual status differs from FSRS state.

The possible ranges are 0.0625–16 for new-card weights, 0.125–110 for mature-review weights, and 0.5–27.5 for learning/relearning weights, before considering cooldown exclusion. Weights are relative lottery mass, not percentages, mastery scores, or FSRS recall probabilities. Worked examples below assume normal personal interest.

### 5. Draw within the chosen pool

For a card $i$ in the selected pool $P$:

$$
\Pr(i\mid P)=\frac{W_i}{\sum_{j\in P}W_j}
$$

The global probability is the selected pool's share times this conditional weight. With no selectable steps, let $S_K$ be the learned share above (zero without learned candidates, one for learned-only), and let $S_N$ be the inner active new share. Then new has global share $(1-S_K)S_N$, nonlearned review has $(1-S_K)(1-S_N)$, and learned has $S_K$. A sole active pool receives the full active remainder. Selectable nonlearned due steps instead have share one and pause every other pool. Cooldown-excluded or not-yet-due cards have zero probability for that call.

The implementation draws a pseudorandom value in $[0,1)$, multiplies it by the pool's total weight, and walks cumulative card weights until that draw is covered. There is no persistent shuffled queue or per-word quota. Every card in the chosen pool has positive weight, but the lottery does not guarantee that a particular card appears within a fixed number of calls.

#### Worked example: weights are not global priorities

Assume these **nonlearned** Review-state cards are due and outside the hard cooldown, with no selectable learning/relearning steps or learned maintenance candidates:

| Card | Overdue / scheduled interval | Failures / lapses | Last presented | Usefulness | Weight |
|---|---|---|---|---|---|
| A | 0 / 2 days | 0 / 0 | Exactly 24 hours ago | `normal` | $1\times1\times1=1$ |
| B | 1 day / 2 days | 1 / 1 | 12 hours ago | `high` | $1.5\times1.75\times0.625=1.640625$ |
| C | 0 / 2 days | 0 / 0 | Exactly 24 hours ago | `low` | $1\times1\times1=1$ |

Total mature-review weight is 3.640625. If that pool is selected, A/B/C have approximately **27.47% / 45.06% / 27.47%** chances. A and C are equally likely because usefulness does not weight reviews. Their overall probabilities additionally depend on the adaptive review-pool share.

For three otherwise equal never-presented new cards with low/normal/high usefulness, weights are 2/4/8 and within-pool probabilities are $1/7,2/7,4/7$. High is twice normal and four times low **within that pool**, not “a 200% chance.”

#### Worked example: cooldown outranks usefulness and the mix

Suppose the only due mature-review card was just presented, while a low-usefulness new card has never been presented. Cooldown removes the review card and preserves the fresh alternative; the new card wins with probability 1, regardless of its low usefulness or the adaptive mix.

Now suppose the only eligible card is that recent due card, and every other card is a future review. The due card repeats. Future cards do not become eligible merely because the due card is on cooldown.

#### Worked example: learning-step priority respects spacing

Suppose a due Learning card is 10 minutes overdue with no failures or lapses, and a due Relearning card is 40 minutes overdue with one consecutive failure and one lapse. Both are selectable. Their weights are $2\times1=2$ and $5\times1.75=8.75$, so their probabilities are $8/43$ and $35/43$. Recent exposure outside the hard cooldown and differing usefulness do not change those weights. All new and mature-review cards have probability zero while either learning step is selectable.

If both steps are instead excluded by the global cooldown and one never-presented new card and one never-presented mature-review card remain, their equal exposure mass gives each pool 50%. Future learning steps do not gain priority before their due time.

### 6. Use the future fallback only when necessary

When there are **no new or due cards at all**:

1. Restrict to nonrecent future cards if any exist; otherwise use all future cards.
2. Prefer nonlearned status over learned maintenance.
3. Within that status-priority group, choose the least recent presentation event ID (never presented = 0), then earliest due time, then card ID.

There is no weighted lottery here. Usefulness, personal interest, urgency, failure counts, and soft recency weights do not change this ordering. Due time is only a tie-breaker, not the primary priority.

For example, among two nonrecent future cards in the same status group, due in one minute and one day with latest presentation IDs 100 and 20, the one-day card wins because it was less recently presented. If neither was presented, the one-minute card wins the due-time tie. A nonlearned card outranks a learned card within the same cooldown eligibility group.

Each issuance moves the selected card to the newest exposure position. Rotation applies within the preferred status group; a future learned word can wait while nonlearned future cards remain. This fallback is offered as optional early practice, not a substitute for due learned maintenance.

The API therefore permits **early reviews** and still records an early presentation. It does not wait until a due date, enforce a daily/session quota, or end the lesson automatically. The normal tutor workflow ends on `reason: "early"` without asking or recording an answer unless the learner explicitly opts into early practice; there is no separate API mode.

### 7. Record the presentation and explain the result

Selection, reading the chosen vocabulary/card, and inserting the presentation happen in one SQLite transaction. The selection clock is captured after acquiring that transaction and reused for due/cooldown decisions, persisted issuance, and the response reason. A queued request therefore observes a card becoming due while it waits. Presenting changes future cooldown/recency; it does not update stability, difficulty, due date, repetitions, or the pending review token.

The response's `reason` is computed **after** choosing the card, in this order:

| First matching condition | `reason` |
|---|---|
| FSRS-new | `new` |
| Due date is in the future | `early` |
| At least 2 consecutive failures **or** at least 3 lapses | `troublesome` |
| At least 1 consecutive failure | `failed` |
| Due before the start of the current **UTC day** | `overdue` |
| Otherwise | `due` |

These labels are not priority queues and are not inputs to the lottery. In particular, a card due earlier today can have urgency greater than 1 while its label remains `due`. A troublesome future card still has `reason: "early"` and can separately have `troublesome: true`.

The presentation's stored `selection_kind` is coarser: only `new`, `due`, or `early`. Tutors should use the chosen card rather than rerolling for a preferred reason or higher usefulness.

## Learned-word reinforcement

`reinforcement_next` is an explicit alternative to scheduled review for stronger production and nuanced usage of **learned** vocabulary. It is not an early FSRS review. The tutor receives the word, saved meaning, all nonempty dated comments from normal and reinforcement reviews, and independent practice state. It asks for a sentence from a description or situation while hiding the target, then guides around prior confusions. The server selects and stores feedback; it does not generate the question or judge answer text.

For meaning $i$, let $C_i$ count nonempty comments from both review channels, $D_i$ be reinforcement difficulty in $[0,4]$ (initially 0), and $R_i$ recover from 0.25 to 1 linearly over 24 hours since its last reinforcement presentation (1 if never presented). Its weight is:

$$
w_i = U_i I_i \left(1+\min(C_i,8)\frac{D_i}{4}\right)(1+D_i)R_i
$$

Both $U_i$ and $I_i$ use low/normal/high multipliers 0.5/1/2. All learned meanings retain positive weights. The bounded comment bonus scales with current difficulty: successful good/easy feedback reduces it, and it disappears at difficulty zero. Historical comments remain available for teaching; their count alone is not evidence of current inability. Comment text is not analyzed automatically. Normal FSRS difficulty, failures, due dates, and learning cooldown are not inputs to this independent lottery.

Group by normalized term. Word weight is the **maximum** of its meaning weights, avoiding an automatic bonus merely for saving more senses. Assign proportional probability, cap any word exceeding 0.25, then redistribute remaining probability proportionally among uncapped words until all comply. Draw a word, then choose one of its learned meanings proportional to meaning weights. `selectionProbability` is the word's probability, summed across all its meanings, not the selected sense's probability.

Before drawing, exclude a whole normalized word for **six hours** after the latest reinforcement issuance among its saved meanings, including archived siblings. This hard cooldown is independent of the 24-hour soft recency weight and the normal learning cooldown. It persists across restart, starts even if no answer follows, and ends at exactly issuance + six hours. Eligibility uses the clock captured after acquiring the write transaction, so a cooldown that expires while a request waits is observed. Future timestamps remain cooling down until that boundary.

At least four distinct learned words must remain outside cooldown. No learned vocabulary returns `NOT_FOUND`; fewer than four eligible words returns `INVALID_ARGUMENT` without issuing or extending anything. Neither cooldown nor the probability cap is relaxed. `eligibleWordCount` counts the post-cooldown pool. Exactly four eligible words forces uniform 25% probabilities; with more, weights influence the capped distribution. With four learned words total, each issuance pauses selection for six hours. The cap is per-call probability, not an empirical session quota; a word may repeat after expiry. Ordinary learning presentations and reinforcement feedback do not start or extend this cooldown.

Each successful next call records a presentation and a fresh independent `reviewToken`. It changes reinforcement recency, not status or FSRS. `reinforcement_review` accepts that token with `again`, `hard`, `good`, or `easy` and an optional factual comment. Difficulty changes by +1/+0.5/−0.5/−1 respectively, clamped to 0–4; review count, last rating, and timestamp persist separately. Independent success reduces both the difficulty multiplier and its share of the comment bonus without deleting history. Answer latency does not change the grade.

Record the first genuine production attempt including usage; guided success cannot erase initial failure. Identical retries return the original saved result without another update, even after demotion; conflicting payloads fail. Normal and reinforcement tokens are not interchangeable. New feedback requires a still-learned item. **Again** changes that meaning to learning, resets mastery without refunding consumed daily credit, and advances a future production due date to now. **Hard** resets mastery but retains learned status. Success does not add mastery credits. These atomic changes never fabricate an FSRS repetition or alter its memory parameters/pending token. Usefulness and personal interest remain explicit preferences, not failure signals.

See [tool inputs and outputs](tools.md#reinforcement_next) and the [standalone reinforcement prompt](prompts.md#learned-word-reinforcement). Raw tool results include the answer: hidden-word practice depends on the tutor and host not exposing that private context.

## How a review changes the schedule

### Review transaction and retry behavior

```mermaid
sequenceDiagram
    participant Tutor as AI tutor
    participant Learner
    participant MCP as MCP service
    participant DB as SQLite transaction
    participant Schedule as FSRS
    Tutor->>MCP: learning_next
    MCP->>DB: Select card, read content, append presentation
    DB-->>MCP: Commit item and pending token T
    MCP-->>Tutor: Item, shownAt, presentationId, token T
    Tutor->>Learner: Ask one production-recall question
    Learner-->>Tutor: Answer
    Tutor->>MCP: learning_review(T, submitted rating, comment)
    MCP->>DB: Find existing attempt for T
    alt Matching attempt already exists
        DB-->>MCP: Original saved result, duplicate = true
    else First submission with a valid active-card token
        DB->>DB: Promote fast good to easy using first presentation and settings
        DB->>Schedule: Card, transaction review time, final rating, settings
        Schedule-->>DB: Updated scheduling state
        DB->>DB: Save attempt and daily mastery, update card/status, rotate token, commit
        DB-->>MCP: New saved result, duplicate = false
    end
    MCP-->>Tutor: nextReviewAt, rating, status, masteryStreak, troublesome
```

The service trims the token and comment and validates the rating. After acquiring its write transaction, persistence checks for an existing attempt **before** resolving the current card or capturing the new review's UTC timestamp:

- Same token and original request fingerprint (submitted rating plus trimmed comment): return the saved result without scheduling again, even if its final grade was promoted.
- Same token with a different rating or comment: `INVALID_ARGUMENT`.
- No existing attempt and no current card for that owner's token: `NOT_FOUND`.
- A current token for an archived item: `INVALID_ARGUMENT`.
- Otherwise: calculate scheduling, save the attempt, update the card, and rotate its token atomically.

Consequently, a valid duplicate can still return its original result after later reviews, archiving, or deletion. It returns that attempt's schedule, not the latest schedule. A failed transaction does not partially record a review.

A due date is not an acceptance gate: a valid active-card token can be reviewed early. A presentation is also not a storage-level prerequisite, although the normal tutor workflow gets the token from `learning_next`. Missing presentation history keeps the submitted grade. Repeated presentations do not restart the fast-answer timer.

`learning_review_update` acquires the same writer lock, resolves the owner's original token, and checks that its attempt has the greatest insertion row ID for that owner **before** considering duplicate corrections. It requires an existing, non-archived card. Scheduling starts from the stored pre-review state at the original review time, not from the current card or correction time. The saved result and card update commit together, preserving the pending token and attempt count. Identical corrections replay without scheduling; older attempts are always rejected. Legacy pre-review clocks are restored only from recorded predecessor reviews with matching repetition counts; missing history causes a safe failure, never an invented timestamp.

Corrections also restore and replay the original mastery streak, last credited UTC day, and status. Changed corrections are rejected after newer reinforcement feedback for the same meaning, detected by a persisted review-count snapshot rather than timestamps. Identical corrections return their saved result without undoing later reinforcement. Legacy reviews lacking status evidence preserve current manual status and do not fabricate mastery.

### One stored rating and fast-answer promotion

Map `again`, `hard`, `good`, and `easy` to grades 1, 2, 3, and 4. Before scheduling a new review, read the first presentation for the same owner, card, and token. Promote only `good` when `0 <= reviewedAt - firstShownAt < fastAnswerSeconds`; the default threshold is 30 seconds and zero disables it. Exactly at the threshold, slower responses, missing presentations, and negative elapsed intervals keep the submitted grade. `again`, `hard`, and `easy` are unchanged at any speed.

The review stores only the final `rating`, used consistently by FSRS, comment history, and analytics. A request fingerprint preserves original-payload retries without retaining a second rating. Migration preserves the actual scheduling grade of older attempts and fingerprints their original payloads, without recalculating schedules.

Explicit `learning_review_update` corrections bypass promotion so an accidental `easy` can be corrected to `good`. Changed corrections use the current algorithm settings with the original pre-review state and timestamp; identical corrections and review retries do not reschedule.

### The FSRS model actually used here

The dependency is [`go-fsrs/v4` at `v4.0.0`](https://github.com/open-spaced-repetition/go-fsrs/tree/v4.0.0), whose implementation uses the **FSRS v6 model**. The service starts with these defaults; retention, maximum interval, and step lists are editable in Admin → Settings:

| Parameter | Default value | Meaning |
|---|---|---|
| Requested retention $q$ | 0.9 | Target modeled recall probability when setting a day-scale interval |
| Maximum due delay $M$ | 36,500 days | Upper bound on the actual day-scale due delay |
| Short-term scheduling | Enabled | Use explicit learning/relearning steps |
| Learning steps | 1 and 10 minutes | Initial learning progression |
| Relearning steps | 10 minutes | Return after a failed review |
| Fuzz | Disabled | No random jitter in the FSRS due date |
| Model weights | Fixed defaults | No per-user fitting or history optimizer runs |

The selector is random; this scheduler is deterministic for the same card state, final rating, settings, and review time.

#### Card memory and counters

| Field | Meaning |
|---|---|
| Stability $S$ | Modeled days until recall probability decays to 0.9 |
| Difficulty $D$ | FSRS memory-model difficulty, constrained to 1–10; not dictionary or CEFR difficulty |
| FSRS state | `New=0`, `Learning=1`, `Review=2`, `Relearning=3` |
| Last review time | Previous accepted review time |
| Remaining steps | Progress through initial learning or relearning steps |
| Scheduled days / due date | The scheduled interval metadata and next due timestamp |
| Repetitions | Number of accepted reviews; duplicate retries do not increment it |
| Lapses | Number of `again` reviews whose **previous** FSRS state was `Review` |
| Consecutive failures | Number of consecutive **submitted** `again` ratings |
| Mastery streak / last mastery time | Separate promotion evidence: flawless daily recall count and last credited UTC day; not FSRS memory-model inputs |

New cards start with zero-valued memory/counters, FSRS `New` state, and a due date at creation. Every accepted review updates memory, increments repetitions, and sets the last review time to now—even in minute-scale learning.

For consecutive failures $c$ and lapses $\ell$:

$$
c' =
\begin{cases}
c+1 & \text{submitted rating is again}\\
0 & \text{otherwise}
\end{cases}
\qquad
\ell'=\ell+
\mathbf{1}[\text{previous state is Review and final rating is again}]
$$

$$
\text{troublesome}=(c'\geq2)\ \lor\ (\ell'\geq3)
$$

`again` in initial Learning or Relearning can extend the failure streak but adds no lapse. A successful response clears the streak, not cumulative lapses. Once a card reaches three lapses it remains troublesome across ordinary successful reviews. This flag tells the tutor to change the cue or address confusion; it does not add a special FSRS scheduling branch.

Vocabulary `new`/`learning`/`learned` labels remain unchanged by these transitions. Archiving blocks practice; reactivation preserves an existing card, including its schedule and history, rather than resetting it.

### State transitions and concrete first-review results

This diagram shows normal reachable states under the default learning steps. All grades shown are **final stored grades**, after any timing promotion.

```mermaid
stateDiagram-v2
    state "New (0)" as NewCard
    state "Learning (1)" as LearningCard
    state "Review (2)" as ReviewCard
    state "Relearning (3)" as RelearningCard
    [*] --> NewCard
    NewCard --> LearningCard: Again / Hard / Good
    NewCard --> ReviewCard: Easy
    LearningCard --> LearningCard: Again / Hard / Good with steps left
    LearningCard --> ReviewCard: Easy or final Good
    ReviewCard --> RelearningCard: Again
    ReviewCard --> ReviewCard: Hard / Good / Easy
    RelearningCard --> RelearningCard: Again / Hard
    RelearningCard --> ReviewCard: Good / Easy
```

**First review of an FSRS-new card:**

| Final rating | Initialized stability | Next state | Due after review | Remaining steps |
|---|---|---|---|---|
| `again` | 0.212 days | Learning | 1 minute | 2 |
| `hard` | 1.2931 days | Learning | 6 minutes | 2 |
| `good` | 2.3065 days | Learning | 10 minutes | 1 |
| `easy` | 8.2956 days | Review | 8 days | 0 |

The initial hard step is $\operatorname{round}((1+10)/2)=6$ minutes, not 5 or 5.5.

**Important consequence:** with default settings a final `good` takes the **10-minute** Learning step, whereas `easy` goes directly to an **8-day** review. A submitted `good` inside the fast-answer window is stored and scheduled as `easy`.

**Subsequent step behavior:**

| Previous state | `again` | `hard` | `good` | `easy` |
|---|---|---|---|---|
| Learning, steps pending | Reset to 2 remaining, due in 1 minute | Keep remaining count, due in 6 minutes | With 2 remaining: 10 minutes and 1 remaining; with 1: graduate | Graduate |
| Relearning, 1 step pending | Keep/reset to 1, due in 10 minutes | Keep 1, due in 15 minutes | Graduate | Graduate |
| Review | Add a lapse; enter Relearning, due in 10 minutes | Stay Review with calculated interval | Stay Review with calculated interval | Stay Review with calculated interval |

“Graduate” means enter Review, clear remaining steps, and use a day-scale interval calculated from updated stability. Stability and difficulty are updated before applying these steps; learning is not just a timer with frozen memory.

Minute steps store `scheduledDays = 0`. All due dates are measured from the **current review time**, not added to the previous due date. For a legacy Learning/Relearning card with no steps remaining, this dependency graduates even on `again`/`hard` rather than restarting steps.

### Forgetting curve and base interval

Define the stability and difficulty clamps:

$$
C_S(x)=\operatorname{clamp}(x,0.001,36500)
\qquad
C_D(x)=\operatorname{clamp}(x,1,10)
$$

For the current model, with zero-indexed weight $w_{20}=0.1542$:

$$
\delta=-w_{20}=-0.1542
\qquad
a=0.9^{1/\delta}-1\approx0.9803464944
$$

$$
R(t,S)=\left(1+a\,\frac{t}{C_S(S)}\right)^\delta
$$

$R$ is modeled retrievability, not observed answer correctness. For valid stability, $R(0,S)=1$ and $R(S,S)=0.9$. Larger stability means slower predicted forgetting.

For example, holding stability at 10 days gives these curve samples. This illustrates the model; it is not a graph of a learner's measured performance:

```mermaid
---
config:
  themeVariables:
    xyChart:
      plotColorPalette: "#2563eb"
---
xychart-beta
    title "Modeled retrievability with stability held at 10 days"
    x-axis "Elapsed days" [0, 5, 10, 15, 20, 25, 30]
    y-axis "Retrievability" 0.8 --> 1
    line [1, 0.94034, 0.9, 0.86983, 0.84588, 0.82614, 0.80939]
```

Solving the curve for requested retention $q$ gives:

$$
I_{\mathrm{raw}}(S,q)
=\frac{C_S(S)}{a}\left(q^{1/\delta}-1\right)
$$

$$
I(S)=\operatorname{clamp}
\left(\operatorname{round}(I_{\mathrm{raw}}(S,0.9)),1,36500\right)
$$

At the configured $q=0.9$, the raw interval simplifies mathematically to $C_S(S)$. The implementation computes the full floating-point expression and rounds positive half-days upward. Minute steps and the Review-state grade-order rules below can supersede this base interval.

#### Two different elapsed-day clocks

Do not substitute the continuously elapsed hours from the selection formulas into the scheduling code. This dependency uses:

| Use | Elapsed days |
|---|---|
| Scheduling memory updates | Difference between the current and previous review's **UTC calendar dates** |
| Retrievability saved in review snapshots | Floor of actual elapsed time divided by 24 hours |

For example, a review at 23:59 UTC followed by one at 00:01 UTC has one elapsed scheduling day but zero full 24-hour periods. Scheduling uses $R(1,S)$, while the stored “before” retrievability can be $R(0,S)=1$.

New cards or missing last-review times report retrievability 0. Immediately after an accepted review, the stored retrievability is 1. That is a snapshot: it does not decay in the database as time passes and is **not fed back** into FSRS or selection. FSRS recomputes the curve from stability and elapsed time.

### Exact FSRS memory-update equations

The following equations describe the enabled implementation in [`arithmetic.go`](https://github.com/open-spaced-repetition/go-fsrs/blob/v4.0.0/arithmetic.go), not an older FSRS formula. Let $g$ now denote the **effective** numeric grade, and let $S,D$ be the previous memory values. Stability expressions use old difficulty $D$, not the newly computed $D'$.

The fixed zero-indexed weight vector is:

```text
w[0..20] = [
  0.212, 1.2931, 2.3065, 8.2956, 6.4133, 0.8334, 3.0194,
  0.001, 1.8722, 0.1666, 0.796, 1.4835, 0.0614, 0.2629,
  1.6483, 0.6014, 1.8729, 0.5425, 0.0912, 0.0658, 0.1542
]
```

**Initialization from New:**

$$
S_0(g)=\operatorname{clamp}(w_{g-1},0.1,36500)
$$

$$
D_{\mathrm{init}}(g)=w_4-e^{w_5(g-1)}+1
\qquad
D_0(g)=C_D(D_{\mathrm{init}}(g))
$$

Initialization has a 0.1-day stability minimum; subsequent stability updates use 0.001.

**Difficulty after every non-New review:**

$$
\Delta D=-w_6(g-3)
$$

$$
D'=C_D\left(
w_7D_{\mathrm{init}}(4)
+(1-w_7)\left[D+\frac{10-D}{9}\Delta D\right]
\right)
$$

The mean-reversion anchor is the **unclamped** initial Easy difficulty, even though it is below 1 with these defaults. The result, not the anchor, is clamped to 1–10.

**Stability when both reviews have the same UTC date, in any non-New state:**

$$
k=e^{w_{17}(g-3+w_{18})}\,C_S(S)^{-w_{19}}
\qquad
k_g=
\begin{cases}
k & g=1\\
\max(k,1) & g\geq2
\end{cases}
$$

$$
S'=C_S(C_S(S)\,k_g)
$$

Same-day `hard`, `good`, and `easy` therefore cannot reduce stability. This short-term branch also applies to Review cards, not just cards in Learning.

**Stability on a later UTC date after successful recall (`hard`, `good`, or `easy`):**

Using $R=R(t_{\mathrm{calendar}},S)$:

$$
S'=C_S\left(
S\left[1+e^{w_8}(11-D)S^{-w_9}
\left(e^{(1-R)w_{10}}-1\right)H(g)E(g)\right]
\right)
$$

Here $H(2)=w_{15}=0.6014$ and $H(g)=1$ otherwise; $E(4)=w_{16}=1.8729$ and $E(g)=1$ otherwise. The hard factor reduces stability growth relative to ordinary recall; the easy factor increases it.

**Stability on a later UTC date after `again`:**

$$
S_{\mathrm{fail}}=
w_{11}D^{-w_{12}}\left[(S+1)^{w_{13}}-1\right]
e^{(1-R)w_{14}}
$$

$$
S'=C_S\left(
\min\left[S_{\mathrm{fail}},\frac{S}{e^{w_{17}w_{18}}}\right]
\right)
$$

These later-date equations also update memory in Learning/Relearning. The previous state separately controls learning steps and whether a lapse is added.

#### Grade ordering for a card already in Review

The scheduler computes candidate updated stabilities $S'_H,S'_G,S'_E$ for the three successful grades and then sets:

$$
i_H=\min(I(S'_H),I(S'_G))
$$

$$
i_G=\max(I(S'_G),i_H+1)
\qquad
i_E=\max(I(S'_E),i_G+1)
$$

This prevents successful grade intervals from collapsing onto the same rounded day in ordinary Review scheduling. New-card Easy and graduation from Learning/Relearning use their own $I(S')$ without this cross-grade ordering.

At the extreme 36,500-day limit, ordering runs after the base cap: stored `scheduledDays` can reach 36,501 or 36,502, while the actual due delay is capped again at 36,500 days. Thus the stored interval is not unconditionally identical to the final due delay at that boundary.

Neither usefulness nor the selection failure multiplier appears in any FSRS equation. Review history influences scheduling through the evolving memory state; the service does not replay the whole history, train weights, or optimize a personal retention target on each review.

## Content precedence during reviews

Card selection finishes before question content is chosen. Content quality, definition length, and dictionary coverage do not decide which card wins.

The server first takes a trimmed custom description, if present, and the first personal example, if present. It then follows this precedence:

1. **Explicit saved sense:** fill any missing definition/example from that sense, even without a linked lookup, then stop. Never borrow an example from a different sense.
2. **No linked lookup, or a custom definition without an explicit sense:** return the personal content. A custom meaning does not borrow arbitrary dictionary examples.
3. **Learner context without an explicit sense or custom definition:** try lexical matching against the linked definitions. Only a unique positive best match supplies a dictionary definition and its own first example. Ties or no content-bearing overlap leave dictionary content absent; preserve personal content and clarify rather than guessing.
4. **No learner context:** take the first dictionary definition in stored order and, if needed, its own first example. If that sense has no example, leave it absent instead of searching other senses.

Non-empty saved `context` is returned separately, including for context-only items with no dictionary content. It distinguishes the intended meaning but need not be a complete definition.

### The context-matching formula

This is word overlap, not an embedding or language-model similarity score:

- Context words come from **saved context + tags + notes + personal examples**.
- Candidate words come from **definition text + guideword**.
- Both sides are lowercased, split on non-ASCII-letter characters, reduced to unique words, and discard words of two letters or fewer and a fixed list of common function words such as `the`, `where`, and `which`. Words from the normalized target term are excluded from context evidence, including hyphen-separated components.

For dictionary definition $d$:

$$
\operatorname{contextScore}(d)
:= \left|\operatorname{words}(\text{context, tags, notes, examples})
\cap \operatorname{words}(d.\text{definition},d.\text{guideword})\right|
$$

Only a positive score can win. The largest overlap must be unique; tied best scores abstain instead of choosing dictionary order. Context containing only function words, the target spelling, or unrelated words also abstains. There is no stemming or semantic matching.

For example, a `boating` context or tag can favor a definition containing “boating,” but does not automatically match “boat” or “sailing.” An explicit selected sense takes precedence over the heuristic.

The latest **non-empty** review comment is returned separately. A newer review without a comment does not erase older useful feedback. `includeComments: true` requests the complete non-empty comment history; it does not change selection, content matching, or scheduling. The AI tutor decides how to use this content and should hide the target term while asking a production-recall question. The [suggested tutor prompts](prompts.md) instruct it to use a different sentence or situation when a meaning returns with a new review token, without changing the saved meaning or rewriting stored examples.

## What affects what

“No direct effect” below means the implementation does not read that input for that decision. An AI tutor can still make an **indirect** change by submitting a different hint or rating, or by choosing when to request/review a card.

This table describes **scheduled `learning_next` / `learning_review`**. The independent reinforcement lottery above additionally uses comment count, usefulness for learned words, and separate practice difficulty.

| Input or action | Next-item selection | FSRS schedule | Content / usefulness |
|---|---|---|---|
| Owner namespace | Determines whose cards/history are eligible | Scopes valid review tokens/history | Scopes saved vocabulary, not corpus scores |
| Archive/delete | Removes eligibility | Blocks new reviews / removes the card | History remains; reactivation preserves an existing card |
| Active status: `new`, `learning`, `learned` | Learned maintenance gets lower adaptive share; active cards retain acquisition priority | Same FSRS formula; accepted feedback updates status and daily mastery | Manual overrides remain available |
| FSRS state | Splits active new, due learning/relearning, review, and future pools; learned status chooses maintenance pool | Chooses initialization/step/review behavior | No usefulness effect |
| Due date and scheduled interval | Eligibility and urgency; due time breaks future exposure ties | Outputs of scheduling; old values are not memory-equation multipliers | No usefulness effect |
| Stored stability, difficulty, last-review time | No direct lottery input; previous schedules affect eligibility/urgency | Core memory inputs | No usefulness effect |
| Effective usefulness | Multiplies only new-card weights by 0.5/1/2 | No direct effect | Derived from term evidence and hint |
| Personal interest | Multiplies all selectable new/due weights by 0.5/1/2; no future-rotation effect | No effect | Explicit preference only; never inferred from failures |
| Term spelling, bundled ranks, expression evidence | Indirectly through calculated usefulness; no separate raw-rank weight | No direct effect | Determine inference matches and votes |
| Optional usefulness hint | Indirectly through effective category | No direct effect | Weight 2 in inference; not a forced override |
| Consecutive failures and lapses | Bounded extra due-card weight; troublesome label | Not direct equation multipliers; rating/state update counters | No usefulness effect |
| Latest presentation and recent event IDs | Global adaptive cooldown; new/mature-review recency weight; future exposure ordering | First presentation for the pending token supplies fast-answer timing | Issuance is not proof of human visibility |
| Repeated `learning_next` calls | Change future selection history, even without an answer | Do not reschedule or restart the fast-answer timer | New presentation event each time |
| Rating | Through updated status, due/state/failures; Again can return learned to active acquisition | Final stored grade is a direct input on every accepted attempt | Good/easy mastery credit at most once per UTC day; every hard/again resets it |
| Answer latency | No separate speed-based selection score | Only fast good can become easy; slow responses are never penalized | Includes model/delivery time, not pure recall time |
| Tags, notes, personal examples | Not filters or weights for `learning_next` | No direct effect | Can choose a fallback definition by word overlap |
| Custom description and selected sense | Do not change lottery weight | No direct effect | Control returned definition/example; sense identity gives an independent card |
| Review comment text / `includeComments` | No selection effect | No direct effect | Feedback for the tutor, not automated scoring |
| CEFR, Cambridge labels, pronunciation, term length | No direct ranking or eligibility effect | No direct effect | Can inform tutor explanation/hint, but not offline inference directly |
| Lookup/cache availability | Dictionary content is not required for selection | No direct effect | Affects available question material, not offline usefulness |
| `vocabulary_list` filters, pagination, sort | Do not carry over into `learning_next` | No effect | Browsing and untracked exercise selection only |
| Stored retrievability | Not used in weights | Snapshot only; recomputed from memory rather than read as input | Not a live mastery score |
| Creation/update timestamps | No oldest-first new-card policy or age bonus | No direct memory-equation effect | Browsing/history; card creation initializes its first due date |
| Tutor's goals, interests, conversation, or learner level | Not read by the selector | Not read by FSRS | Only explicit tool inputs can communicate an assessment |
| Anki ratings, intervals, and deck settings | No effect | No effect on this MCP's FSRS | Independent Anki scheduling; sync publishes content one way |

### Practical consequences

- **Marking a word `learned` does not stop reviews.** Archive it to remove it from practice.
- **Within scheduled review, usefulness only prioritizes new introductions within their pool.** Even a high-usefulness new card can be on cooldown, paused behind selectable learning steps, in the unchosen pool, or lose the lottery. In the separate reinforcement mode, usefulness also weights learned words.
- **A troublesome card does not always outrank everything.** Its due-card weight is larger but capped, and the usual pool/fallback rules still apply.
- **Repeated spelling does not always mean a failed cooldown.** Different saved meanings have different cards; cooldown is card-based.
- **Skipping an answer is not a failed review.** Presentation affects selection history, but only a submitted review updates FSRS and failure counters.
- **Waiting hours is not graded as hesitation.** The timing rule only promotes quick `good` responses; it never lowers a grade.
- **In mixed mode, completing all due cards does not make `learning_next` empty.** It can return new items or early reviews until the tutor/learner stops.
- **Daily mastery and review quotas are different.** Success earns at most one mastery credit per UTC day, while every genuine graded attempt is recorded. Due steps take priority; otherwise learned maintenance receives 10%–40% against active work, whose remaining new/review split uses 20%–80% exposure bounds. There is no enforced lesson length or guarantee that every due card is covered.

### Which parameters can a caller change?

Tool callers can save/archive items, update usefulness hints, personal interest and metadata, submit scheduled or reinforcement ratings/comments, and decide when to request another item. They can also manage a persistent batch through `learning_focus`. They cannot pass a topic filter, seed, retention target, new-card percentage, cooldown duration, or FSRS parameter vector to `learning_next`.

The admin can change the fast-answer threshold, mastery requirements, active/learned pool-share bounds, cooldown window/count, exposure recovery/unseen bonus, usefulness/interest multipliers, troublesome thresholds, reinforcement cooldown/cap, and FSRS retention/maximum interval/step lists. Values persist per owner with optimistic revisions; next-item likelihood previews use the same snapshot policy as the selector. [Configuration](configuration.md#runtime-algorithm-settings) lists the defaults and explains application timing. FSRS fitted weights, fuzz-off policy, and low-level equation shapes remain fixed; deployment credentials and listeners remain environment configuration.

## Implementation map

These are the source-of-truth entry points for checking or changing the documented behavior:

| Algorithm / contract | Implementation |
|---|---|
| MCP tool schemas and handlers | [`internal/mcpserver/server.go`](../internal/mcpserver/server.go), [`types.go`](../internal/mcpserver/types.go), [`schema.go`](../internal/mcpserver/schema.go) |
| Term normalization | [`domain.NormalizeTerm`](../internal/domain/text.go) |
| Vocabulary validation, sense identity, metadata | [`internal/vocabulary/service.go`](../internal/vocabulary/service.go) |
| Hint/result persistence | [`SaveVocabulary`, `UpdateVocabulary`](../internal/storage/vocabulary.go) |
| Word-rank votes and exact thirds | [`Estimate`, `estimateRanks`, `rankVote`](../internal/usefulness/usefulness.go) |
| Expression votes, matching tiers, templates, typos | [`expressionMatcher.votes`, `newExpressionMatcher`](../internal/usefulness/expressions.go) |
| Source preparation and limitations | [`build_usefulness.py`](../scripts/build_usefulness.py), [`build_expressions.py`](../scripts/build_expressions.py), [rank manifest](../internal/usefulness/assets/manifest.json), [expression manifest](../internal/usefulness/assets/expressions-manifest.json) |
| Revision-gated usefulness backfill | [`refreshUsefulness`](../internal/storage/usefulness_refresh.go) |
| Candidate pools, cooldown, lottery, weights, fallbacks | [`loadSelectionCards`, `selectLearningCard`, `selectionWeight`, `presentedBefore`](../internal/storage/selection.go) |
| Presentation transaction and review idempotency | [`NextLearningItem`, `RecordReview`](../internal/storage/learning.go) |
| Runtime parameter validation and persistence | [`internal/settings/settings.go`](../internal/settings/settings.go), [`internal/storage/settings.go`](../internal/storage/settings.go) |
| Learned-word capped lottery and independent feedback | [`internal/storage/reinforcement.go`](../internal/storage/reinforcement.go), [`internal/learning/reinforcement.go`](../internal/learning/reinforcement.go) |
| Final-grade FSRS adapter, reasons, question content | [`Service.schedule`, `selectionReason`, `tutoringContent`, `contextualDefinition`](../internal/learning/service.go) |
| Actual FSRS equations and state machine | Dependency [`arithmetic.go`](https://github.com/open-spaced-repetition/go-fsrs/blob/v4.0.0/arithmetic.go), [`scheduler_basic.go`](https://github.com/open-spaced-repetition/go-fsrs/blob/v4.0.0/scheduler_basic.go) |
| FSRS defaults and elapsed-time helpers | Dependency [`parameters.go`](https://github.com/open-spaced-repetition/go-fsrs/blob/v4.0.0/parameters.go), [`weights.go`](https://github.com/open-spaced-repetition/go-fsrs/blob/v4.0.0/weights.go), [`steps.go`](https://github.com/open-spaced-repetition/go-fsrs/blob/v4.0.0/steps.go), [`fsrs.go`](https://github.com/open-spaced-repetition/go-fsrs/blob/v4.0.0/fsrs.go) |

Existing regression scenarios live in [`selection_test.go`](../internal/storage/selection_test.go), [`presentations_test.go`](../internal/storage/presentations_test.go), [`learning/service_test.go`](../internal/learning/service_test.go), [`usefulness_test.go`](../internal/usefulness/usefulness_test.go), and [`expressions_test.go`](../internal/usefulness/expressions_test.go). They cover policy boundaries; the formulas above describe conditional behavior, not a promise of a particular random session sequence.
