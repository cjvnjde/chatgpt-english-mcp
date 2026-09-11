import { createMemo, createResource, For, Show } from "solid-js";
import type { API } from "../api";
import type { AnalyticsData } from "../types";
import { display } from "../format";

export default function Analytics(props: {
  api: API;
  revision: number;
  open: (id: string) => void;
}) {
  const [data, { refetch }] = createResource(
    () => props.revision,
    () => props.api<AnalyticsData>("/analytics"),
  );
  const safe = () => (data.error ? undefined : data());
  const total = () =>
    safe()?.statuses.reduce((sum, r) => sum + r.count, 0) || 0;
  const reviews = () =>
    safe()?.ratings.reduce((sum, r) => sum + r.count, 0) || 0;
  const recall = () =>
    reviews()
      ? Math.round(
          ((safe()
            ?.ratings.filter((r) => ["good", "easy"].includes(r.label))
            .reduce((sum, r) => sum + r.count, 0) || 0) /
            reviews()) *
            100,
        ) + "%"
      : "—";
  const days = createMemo(() =>
    Array.from({ length: 30 }, (_, i) => {
      const d = new Date();
      d.setUTCDate(d.getUTCDate() - 29 + i);
      const day = d.toISOString().slice(0, 10);
      return (
        safe()?.activity.find((x) => x.day === day) || {
          day,
          reviews: 0,
          recalled: 0,
        }
      );
    }),
  );
  return (
    <>
      <div class="page-heading">
        <div>
          <h1>Learning overview</h1>
          <p>
            Vocabulary and review activity for{" "}
            {safe()?.owner || "your configured owner"}.
          </p>
        </div>
        <button disabled={data.loading} onClick={() => void refetch()}>
          Refresh
        </button>
      </div>
      <Show when={data.error}>
        <div class="alert error" role="alert">
          {data.error?.message}
          <button onClick={() => void refetch()}>Retry</button>
        </div>
      </Show>
      <Show when={data.loading}>
        <div class="loading" role="status">
          Loading analytics…
        </div>
      </Show>
      <Show when={safe() && !data.loading}>
        <div class="stats">
          <div>
            <span>Vocabulary</span>
            <strong>{total().toLocaleString()}</strong>
            <small>All learning states</small>
          </div>
          <div>
            <span>Due now</span>
            <strong>{safe()!.due[0]?.count || 0}</strong>
            <small>Active learning cards</small>
          </div>
          <div>
            <span>Reviews</span>
            <strong>{reviews().toLocaleString()}</strong>
            <small>{safe()!.comments[0]?.count || 0} with comments</small>
          </div>
          <div>
            <span>Good / easy</span>
            <strong>{recall()}</strong>
            <small>Share of submitted ratings</small>
          </div>
        </div>
        <div class="analytics-grid">
          <section class="panel chart-panel">
            <h2>Vocabulary by status</h2>
            <For each={["new", "learning", "learned", "archived"]}>
              {(state) => {
                const count = () =>
                  safe()!.statuses.find((r) => r.label === state)?.count || 0;
                return (
                  <div class="bar-row">
                    <span class="badge" data-value={state}>
                      {state}
                    </span>
                    <meter
                      min="0"
                      max={Math.max(total(), 1)}
                      value={count()}
                      aria-label={`${state} vocabulary`}
                    />
                    <strong>{count()}</strong>
                  </div>
                );
              }}
            </For>
          </section>
          <section class="panel chart-panel">
            <h2>Submitted ratings</h2>
            <For each={["again", "hard", "good", "easy"]}>
              {(rating) => {
                const count = () =>
                  safe()!.ratings.find((r) => r.label === rating)?.count || 0;
                return (
                  <div class="bar-row">
                    <span class="badge" data-value={rating}>
                      {rating}
                    </span>
                    <meter
                      min="0"
                      max={Math.max(reviews(), 1)}
                      value={count()}
                      aria-label={`${rating} ratings`}
                    />
                    <strong>{count()}</strong>
                  </div>
                );
              }}
            </For>
            <details>
              <summary>Effective scheduling ratings</summary>
              <p class="muted">
                New reviews use the submitted rating unchanged. Historical reviews
                may retain a different effective rating from an older policy.
              </p>
              <For each={safe()!.effectiveRatings}>
                {(r) => (
                  <p>
                    {r.label}: {r.count}
                  </p>
                )}
              </For>
            </details>
          </section>
        </div>
        <section class="panel chart-panel">
          <h2>
            Review activity <span class="muted">Last 30 days · UTC</span>
          </h2>
          <div class="activity-bars">
            <For each={days()}>
              {(day) => (
                <div
                  title={`${day.day}: ${day.reviews} reviews, ${day.recalled} good/easy`}
                >
                  <meter
                    min="0"
                    max={Math.max(1, ...days().map((x) => x.reviews))}
                    value={day.reviews}
                    aria-label={`${day.day}: ${day.reviews} reviews`}
                  />
                  <small>{day.day.slice(8)}</small>
                </div>
              )}
            </For>
          </div>
          <details>
            <summary>Daily counts</summary>
            <div class="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>Date (UTC)</th>
                    <th>Reviews</th>
                    <th>Good / easy</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={days()}>
                    {(d) => (
                      <tr>
                        <td>{d.day}</td>
                        <td>{d.reviews}</td>
                        <td>{d.recalled}</td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
          </details>
        </section>
        <section class="panel chart-panel">
          <h2>Words to watch</h2>
          <p class="muted">
            Active items with the most lapses, then consecutive failures and
            difficulty.
          </p>
          <Show
            when={safe()!.difficult.length}
            fallback={<div class="empty">No active learning cards yet.</div>}
          >
            <div class="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>Word</th>
                    <th>Lapses</th>
                    <th>Consecutive failures</th>
                    <th>Difficulty</th>
                  </tr>
                </thead>
                <tbody>
                  <For each={safe()!.difficult}>
                    {(r) => (
                      <tr>
                        <td>
                          <button
                            class="row-link"
                            onClick={() => props.open(String(r.id))}
                          >
                            {display(r.term)}
                          </button>
                        </td>
                        <td>{display(r.lapses)}</td>
                        <td>{display(r.consecutive_failures)}</td>
                        <td>{Number(r.difficulty).toFixed(2)}</td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
          </Show>
        </section>
      </Show>
    </>
  );
}
