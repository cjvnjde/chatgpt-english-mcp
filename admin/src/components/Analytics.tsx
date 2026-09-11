import { createMemo, createResource, For, Show } from "solid-js";
import type { API } from "../api";
import type { AnalyticsData } from "../types";
import { display } from "../format";
import { activityDays, wordsNeedingAttention } from "../dashboard";

export default function Analytics(props: {
  api: API;
  revision: number;
  open: (id: string) => void;
  overview: boolean;
  create: () => void;
  navigate: (view: string, column?: string, value?: string) => void;
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
  const days = createMemo(() => activityDays(safe()?.activity || []));
  const today = () => days().at(-1)!;
  const activeDays = () => days().filter((day) => day.reviews > 0).length;
  const activityPeak = () => Math.max(1, ...days().map((day) => day.reviews));
  const attention = () => wordsNeedingAttention(safe()?.difficult || []);
  return (
    <>
      <div class="page-heading">
        <div>
          <span class="eyebrow">
            {props.overview ? "Your workspace" : "Learning insights"}
          </span>
          <h1>{props.overview ? "Overview" : "Learning analytics"}</h1>
          <p>
            Vocabulary and review activity for{" "}
            {safe()?.owner || "your configured owner"}.
          </p>
        </div>
        <div class="toolbar">
          <button disabled={data.loading} onClick={() => void refetch()}>
            Refresh
          </button>
          <Show when={props.overview}>
            <button class="primary" onClick={props.create}>
              + Add word
            </button>
          </Show>
        </div>
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
        <Show when={props.overview}>
          <section class="overview-focus" aria-label="Today in your vocabulary">
            <div>
              <span class="eyebrow">Today · UTC</span>
              <h2>
                {safe()!.due[0]?.count
                  ? `${safe()!.due[0].count.toLocaleString()} cards are due for review.`
                  : total()
                    ? "You’re up to date on due reviews."
                    : "Start with a word worth remembering."}
              </h2>
              <p>
                {total()
                  ? "See what the scheduler may suggest next, or give a difficult word a little more context."
                  : "Add your first word or expression. Your learning activity will appear here."}
              </p>
              <button
                class="primary"
                onClick={() =>
                  total() ? props.navigate("suggestions") : props.create()
                }
              >
                {total()
                  ? "Explore next suggestions →"
                  : "Add your first word →"}
              </button>
              <small>
                Reviews happen with your connected tutor. This workspace is for
                managing and exploring.
              </small>
            </div>
            <div class="today-summary">
              <strong>{today().reviews.toLocaleString()}</strong>
              <span>reviews today</span>
              <small>
                {activeDays()} active {activeDays() === 1 ? "day" : "days"} in
                the last 30
              </small>
            </div>
          </section>
          <div class="overview-links" aria-label="Workspace shortcuts">
            <button
              onClick={() =>
                props.navigate("vocabulary_items", "owner_key", safe()!.owner)
              }
            >
              <span>Browse your vocabulary</span>
              <span aria-hidden="true">↗</span>
            </button>
            <button
              onClick={() =>
                props.navigate("comments", "owner_key", safe()!.owner)
              }
            >
              <span>Read tutor comments</span>
              <span aria-hidden="true">↗</span>
            </button>
            <button onClick={() => props.navigate("analytics")}>
              <span>Explore learning analytics</span>
              <span aria-hidden="true">↗</span>
            </button>
          </div>
        </Show>
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
        <Show when={!props.overview}>
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
                  New reviews use the submitted rating unchanged. Historical
                  reviews may retain a different effective rating from an older
                  policy.
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
        </Show>
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
                  <div
                    class="activity-track"
                    role="img"
                    aria-label={`${day.day}: ${day.reviews} reviews, ${day.recalled} good/easy`}
                  >
                    <span
                      class="activity-fill"
                      style={{
                        height: `${(day.reviews / activityPeak()) * 100}%`,
                      }}
                    />
                  </div>
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
          <h2>Words needing attention</h2>
          <p class="muted">
            Words with recorded lapses or consecutive failures. Open one to add
            a helpful note or example.
          </p>
          <Show
            when={attention().length}
            fallback={
              <div class="empty">
                <h2>No struggling words right now</h2>
                <p>
                  Words with recorded lapses or repeated failures will appear
                  here.
                </p>
              </div>
            }
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
                  <For
                    each={
                      props.overview ? attention().slice(0, 5) : attention()
                    }
                  >
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
