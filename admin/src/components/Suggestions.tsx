import {
  batch,
  createEffect,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import type { API } from "../api";
import type { Suggestion, SuggestionsPage } from "../types";
import { date, label } from "../format";
import { chance, chanceWidth } from "../suggestionDisplay";

const reasons: Record<Suggestion["reason"], string> = {
  new: "New word",
  due: "Due now",
  early: "Early review fallback",
  cooldown: "Recently shown · cooling down",
  learning_first: "Due learning steps come first",
  not_due: "Not due yet",
  waiting: "Waiting for an earlier-priority card",
};
const pools: Record<Suggestion["pool"], string> = {
  new: "New",
  learning: "Learning / relearning",
  review: "Review",
};
export default function Suggestions(props: {
  api: API;
  revision: number;
  open: (id: string) => void;
  create: () => void;
}) {
  const [limit, setLimit] = createSignal(50);
  const [offset, setOffset] = createSignal(0);
  const [page, { refetch }] = createResource(
    () =>
      `${new URLSearchParams({
        limit: String(limit()),
        offset: String(offset()),
      })}&revision=${props.revision}`,
    (params) => props.api<SuggestionsPage>(`/suggestions?${params}`),
  );
  const safe = () => (page.error ? undefined : page());
  const maximum = () =>
    Math.max(0, ...(safe()?.rows.map((row) => row.probability) || []));
  createEffect(() => {
    const current = safe();
    if (current && !page.loading && offset() > 0 && !current.rows.length)
      setOffset(
        Math.max(0, Math.floor((current.total - 1) / limit()) * limit()),
      );
  });
  return (
    <>
      <div class="page-heading">
        <div>
          <span class="eyebrow">A look ahead</span>
          <h1>Next suggestions</h1>
          <p>
            Words ranked by their chance of being suggested next. Selection is
            randomized, so this is a live priority preview, not a fixed queue.
          </p>
        </div>
        <button disabled={page.loading} onClick={() => void refetch()}>
          Refresh
        </button>
      </div>
      <section
        class="suggestion-explainer"
        aria-label="How suggestions are selected"
      >
        <span class="badge">Read-only preview</span>
        <p>Exploring this list never presents a word or records a review.</p>
        <details>
          <summary>How selection works</summary>
          <p class="muted">
            Chances change as words are shown, reviews are recorded, or time
            passes. Refresh to see the latest state.
          </p>
          <p class="muted">
            Due learning and relearning steps take priority. Otherwise, when
            both new words and due reviews are available, the scheduler gives
            new words a 20% share and reviews an 80% share. Within each group,
            weights account for usefulness of new words, personal interest,
            review urgency, failures, and recent exposure.
          </p>
          <p class="muted">
            Recently shown words cool down when alternatives exist. If nothing
            is new or due, an early review is selected. A 0% chance means the
            word cannot be selected in this snapshot, not that it will never be
            suggested. Ties are displayed by due date, then card ID; ranks are
            not future turn numbers.
          </p>
        </details>
      </section>
      <Show when={page.error}>
        <div class="alert error" role="alert">
          {String(page.error?.message || page.error)}
          <button onClick={() => void refetch()}>Retry</button>
        </div>
      </Show>
      <Show when={page.loading}>
        <div class="loading" role="status">
          Loading suggestions…
        </div>
      </Show>
      <Show when={safe() && !page.loading}>
        <section class="panel" aria-label="Suggested word ranking">
          <div class="suggestion-summary" role="status">
            <div>
              <strong>{safe()!.selectable.toLocaleString()}</strong>
              <span>eligible next</span>
            </div>
            <div>
              <strong>{safe()!.total.toLocaleString()}</strong>
              <span>active cards</span>
            </div>
            <div class="snapshot-info">
              <small>Snapshot: {date(safe()!.generatedAt)}</small>
              <small>Owner: {safe()!.owner}</small>
            </div>
          </div>
          <Show when={safe()!.rows.length}>
            <div class="probability-legend">
              <span>
                Bars compare chances on this page. Percentages are the actual
                chance of the next draw.
              </span>
              <strong>Scale: 0–{chance(maximum())}</strong>
            </div>
          </Show>
          <Show
            when={safe()!.rows.length}
            fallback={
              <div class="empty">
                <h2>No active words to suggest</h2>
                <p>Add vocabulary or unarchive a word to start learning.</p>
                <button class="primary" onClick={props.create}>
                  + Add word
                </button>
              </div>
            }
          >
            <div
              class="table-scroll suggestion-scroll"
              tabIndex={0}
              aria-label="Suggestions; scroll horizontally for more columns"
            >
              <table>
                <thead>
                  <tr>
                    <th scope="col">Rank now</th>
                    <th scope="col">Word / meaning</th>
                    <th scope="col">Chance next</th>
                    <th scope="col">Why this word</th>
                    <th scope="col">Timing</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={safe()!.rows}>
                    {(row, index) => (
                      <>
                        <Show
                          when={
                            row.probability === 0 &&
                            (index() === 0 ||
                              safe()!.rows[index() - 1].probability > 0)
                          }
                        >
                          <tr class="group-divider">
                            <td colspan="5">
                              Not eligible right now{" "}
                              <span>· 0% chance in this snapshot</span>
                            </td>
                          </tr>
                        </Show>
                        <tr classList={{ unavailable: row.probability === 0 }}>
                          <td class="rank-cell">
                            {row.probability > 0
                              ? safe()!.offset + index() + 1
                              : "—"}
                          </td>
                          <td>
                            <button
                              class="row-link"
                              onClick={() => props.open(row.vocabularyItemId)}
                            >
                              {row.term}
                            </button>
                            <Show when={row.context}>
                              <small class="term-context">{row.context}</small>
                            </Show>
                            <div class="word-meta">
                              <span>{label(row.status)}</span>
                              <span>· {label(row.usefulness)} usefulness</span>
                            </div>
                          </td>
                          <td class="probability-cell">
                            <strong>{chance(row.probability)}</strong>
                            <div class="probability-track" aria-hidden="true">
                              <span
                                style={{
                                  width: `${chanceWidth(row.probability, maximum())}%`,
                                }}
                              />
                            </div>
                          </td>
                          <td>
                            <span
                              class="badge reason-badge"
                              data-value={row.reason}
                            >
                              {reasons[row.reason]}
                            </span>
                            <small class="term-context">
                              {pools[row.pool]}
                            </small>
                          </td>
                          <td class="timing-cell">
                            <span>Due {date(row.dueAt)}</span>
                            <small>
                              {row.lastShownAt
                                ? `Last shown ${date(row.lastShownAt)}`
                                : "Not shown yet"}
                            </small>
                          </td>
                        </tr>
                      </>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
          </Show>
          <div class="pagination">
            <span>
              {safe()!.total ? safe()!.offset + 1 : 0}–
              {safe()!.offset + safe()!.rows.length}
              {" of "}
              {safe()!.total.toLocaleString()} words
              <small>
                Times shown in{" "}
                {Intl.DateTimeFormat().resolvedOptions().timeZone}
              </small>
            </span>
            <div class="toolbar">
              <label>
                Rows
                <select
                  value={limit()}
                  onChange={(e) =>
                    batch(() => {
                      setOffset(0);
                      setLimit(Number(e.currentTarget.value));
                    })
                  }
                >
                  <option>25</option>
                  <option>50</option>
                  <option>100</option>
                  <option>200</option>
                </select>
              </label>
              <button
                disabled={offset() === 0}
                onClick={() => setOffset(Math.max(0, offset() - limit()))}
              >
                Previous
              </button>
              <button
                disabled={offset() + safe()!.rows.length >= safe()!.total}
                onClick={() => setOffset(offset() + limit())}
              >
                Next
              </button>
            </div>
          </div>
        </section>
      </Show>
    </>
  );
}
