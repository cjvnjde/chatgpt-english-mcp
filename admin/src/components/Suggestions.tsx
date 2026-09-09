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
import { date } from "../format";

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
const percent = new Intl.NumberFormat(undefined, {
  style: "percent",
  maximumFractionDigits: 2,
});
function chance(probability: number) {
  return probability > 0 && probability < 0.0001
    ? `<${percent.format(0.0001)}`
    : percent.format(probability);
}

export default function Suggestions(props: {
  api: API;
  revision: number;
  open: (id: string) => void;
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
        class="panel chart-panel"
        aria-label="How suggestions are selected"
      >
        <p>
          Read-only preview: opening this page does not present or review any
          words. Chances change as words are shown, reviews are recorded, or
          time passes. Refresh to see the latest state.
        </p>
        <details>
          <summary>How to read this order</summary>
          <p class="muted">
            Due learning and relearning steps take priority. Otherwise, when
            both new words and due reviews are available, the scheduler gives
            new words a 20% share and reviews an 80% share. Within each group,
            weights account for usefulness of new words, review urgency,
            failures, and recent exposure.
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
          <div class="table-tools">
            <p class="muted" role="status">
              {safe()!.selectable.toLocaleString()} eligible for the next draw
              {" · "}
              {safe()!.total.toLocaleString()} active cards
              {" · "}Owner: {safe()!.owner}
              <br />
              <small>Snapshot: {date(safe()!.generatedAt)}</small>
            </p>
          </div>
          <Show
            when={safe()!.rows.length}
            fallback={
              <div class="empty">
                <h2>No active words to suggest</h2>
                <p>Add vocabulary or unarchive a word to start learning.</p>
              </div>
            }
          >
            <div
              class="table-scroll"
              tabIndex={0}
              aria-label="Suggestions; scroll horizontally for more columns"
            >
              <table>
                <thead>
                  <tr>
                    <th scope="col">Rank now</th>
                    <th scope="col">Word / meaning</th>
                    <th scope="col">Chance next</th>
                    <th scope="col">Selection reason</th>
                    <th scope="col">Learning group</th>
                    <th scope="col">Usefulness</th>
                    <th scope="col">Due</th>
                    <th scope="col">Last shown</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={safe()!.rows}>
                    {(row, index) => (
                      <tr>
                        <td>
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
                            <br />
                            <small>{row.context}</small>
                          </Show>
                        </td>
                        <td>{chance(row.probability)}</td>
                        <td>{reasons[row.reason]}</td>
                        <td>{pools[row.pool]}</td>
                        <td>
                          <span class="badge" data-value={row.usefulness}>
                            {row.usefulness}
                          </span>
                        </td>
                        <td>{date(row.dueAt)}</td>
                        <td>{date(row.lastShownAt)}</td>
                      </tr>
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
