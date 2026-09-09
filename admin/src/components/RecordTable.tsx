import {
  batch,
  createEffect,
  createMemo,
  createResource,
  createSignal,
  For,
  Show,
} from "solid-js";
import type { API } from "../api";
import type { Page, Row, Table } from "../types";
import { date, display, download, label } from "../format";

const preferred: Record<string, string[]> = {
  vocabulary_items: [
    "term",
    "context",
    "learning_status",
    "usefulness",
    "custom_description",
    "tags_json",
    "updated_at",
  ],
  review_attempts: [
    "_term",
    "rating",
    "effective_rating",
    "comment",
    "reviewed_at",
  ],
  learning_presentations: [
    "_term",
    "selection_kind",
    "shown_at",
    "due_at",
    "review_token",
  ],
  learning_cards: [
    "_term",
    "due_at",
    "last_rating",
    "repetitions",
    "lapses",
    "stability",
    "difficulty",
  ],
  dictionary_snapshots: [
    "normalized_term",
    "provider",
    "status",
    "active",
    "fetched_at",
  ],
};
const defaultSort: Record<string, string> = {
  vocabulary_items: "updated_at",
  review_attempts: "reviewed_at",
  learning_presentations: "id",
  learning_cards: "due_at",
  dictionary_snapshots: "fetched_at",
};

export default function RecordTable(props: {
  api: API;
  table: Table;
  revision: number;
  owner: string;
  initialFilter?: { column: string; value: string };
  commentsOnly?: boolean;
  open: (row: Row) => void;
  inspect: (row: Row) => void;
  create: () => void;
}) {
  const tableColumns = createMemo(() => props.table.columns.map((c) => c.name));
  const allColumns = createMemo(() => [
    ...(tableColumns().includes("vocabulary_item_id") ? ["_term"] : []),
    ...tableColumns(),
  ]);
  const [columns, setColumns] = createSignal(
    preferred[props.table.name] || allColumns().slice(0, 6),
  );
  const [search, setSearch] = createSignal("");
  const [query, setQuery] = createSignal("");
  const [column, setColumn] = createSignal(props.initialFilter?.column || "");
  const [value, setValue] = createSignal(props.initialFilter?.value || "");
  const [comments, setComments] = createSignal(props.commentsOnly || false);
  const [sort, setSort] = createSignal(
    defaultSort[props.table.name] || props.table.columns[0].name,
  );
  const [direction, setDirection] = createSignal("desc");
  const [from, setFrom] = createSignal("");
  const [to, setTo] = createSignal("");
  const [limit, setLimit] = createSignal(50);
  const [offset, setOffset] = createSignal(0);
  const [schema, setSchema] = createSignal(false);
  const params = createMemo(() => {
    const p = new URLSearchParams({
      q: query(),
      column: column(),
      value: value(),
      sort: sort(),
      direction: direction(),
      from: from(),
      to: to(),
      comments: String(comments()),
      limit: String(limit()),
      offset: String(offset()),
    });
    return `${p}&revision=${props.revision}`;
  });
  const [page, { refetch }] = createResource(params, (p) =>
    props.api<Page>(`/tables/${encodeURIComponent(props.table.name)}?${p}`),
  );
  const change = (fn: () => void) => {
    batch(() => {
      setOffset(0);
      fn();
    });
  };
  const dateFilter = () =>
    props.table.columns.some((c) =>
      [
        "reviewed_at",
        "shown_at",
        "created_at",
        "fetched_at",
        "applied_at",
      ].includes(c.name),
    );
  createEffect(() => {
    if (
      !page.error &&
      page() &&
      !page.loading &&
      offset() > 0 &&
      page()!.rows.length === 0
    )
      setOffset(
        Math.max(0, Math.floor((page()!.total - 1) / limit()) * limit()),
      );
  });
  const cell = (row: Row, key: string) => {
    const v = row[key];
    if (key.endsWith("_at")) return date(v);
    if (key.endsWith("_json") && typeof v === "string") {
      try {
        const parsed = JSON.parse(v);
        if (Array.isArray(parsed)) return parsed.join(" · ") || "—";
      } catch {
        /* Raw value remains inspectable. */
      }
    }
    return display(v);
  };
  return (
    <>
      <div class="page-heading">
        <div>
          <h1>{label(props.table.name)}</h1>
          <p>
            {props.table.name === "vocabulary_items"
              ? "Manage words, meanings, notes, examples, and tags."
              : props.table.name === "review_attempts"
                ? "Ratings and tutor comments for every recorded review. Answer text is not stored."
                : "Browse records and open a row to inspect every stored field."}
          </p>
        </div>
        <div class="toolbar">
          <button onClick={() => void refetch()} disabled={page.loading}>
            Refresh
          </button>
          <Show when={props.table.name === "vocabulary_items"}>
            <button class="primary" onClick={props.create}>
              + Add word
            </button>
          </Show>
        </div>
      </div>
      <div class="panel">
        <div class="table-tools">
          <form
            class="search"
            onSubmit={(e) => {
              e.preventDefault();
              change(() => setQuery(search()));
            }}
          >
            <input
              aria-label="Search all fields or word"
              placeholder="Search all fields or word…"
              value={search()}
              onInput={(e) => setSearch(e.currentTarget.value)}
            />
            <button type="submit">Search</button>
          </form>
          <label>
            Filter by
            <select
              value={column()}
              onChange={(e) =>
                change(() => {
                  setColumn(e.currentTarget.value);
                  setValue("");
                })
              }
            >
              <option value="">All records</option>
              <For each={tableColumns()}>
                {(c) => <option value={c}>{label(c)}</option>}
              </For>
            </select>
          </label>
          <Show when={column()}>
            <label>
              Exact value
              <input
                value={value()}
                onInput={(e) => change(() => setValue(e.currentTarget.value))}
                placeholder="Value to match"
              />
            </label>
          </Show>
          <Show when={props.table.name === "review_attempts"}>
            <label class="check">
              <input
                type="checkbox"
                checked={comments()}
                onChange={(e) =>
                  change(() => setComments(e.currentTarget.checked))
                }
              />{" "}
              With comments
            </label>
          </Show>
        </div>
        <div class="table-tools secondary-tools">
          <Show when={dateFilter()}>
            <label>
              From (UTC)
              <input
                type="date"
                value={from()}
                onChange={(e) => change(() => setFrom(e.currentTarget.value))}
              />
            </label>
            <label>
              Through (UTC)
              <input
                type="date"
                value={to()}
                min={from()}
                onChange={(e) => change(() => setTo(e.currentTarget.value))}
              />
            </label>
          </Show>
          <label>
            Sort
            <select
              value={sort()}
              onChange={(e) => change(() => setSort(e.currentTarget.value))}
            >
              <For each={tableColumns()}>
                {(c) => <option value={c}>{label(c)}</option>}
              </For>
            </select>
          </label>
          <label>
            Order
            <select
              value={direction()}
              onChange={(e) =>
                change(() => setDirection(e.currentTarget.value))
              }
            >
              <option value="desc">Descending</option>
              <option value="asc">Ascending</option>
            </select>
          </label>
          <details class="column-picker">
            <summary>Columns</summary>
            <div>
              <For each={allColumns()}>
                {(c) => (
                  <label class="check">
                    <input
                      type="checkbox"
                      checked={columns().includes(c)}
                      disabled={columns().length === 1 && columns().includes(c)}
                      onChange={(e) =>
                        setColumns((previous) =>
                          e.currentTarget.checked
                            ? [...previous, c]
                            : previous.length > 1
                              ? previous.filter((x) => x !== c)
                              : previous,
                        )
                      }
                    />
                    {label(c)}
                  </label>
                )}
              </For>
            </div>
          </details>
          <button onClick={() => setSchema(!schema())}>
            {schema() ? "Hide schema" : "Schema"}
          </button>
          <button
            disabled={!!page.error || !page() || page.loading}
            onClick={() =>
              download(
                `${props.table.name}-page-${offset() / limit() + 1}.json`,
                {
                  table: props.table.name,
                  filters: Object.fromEntries(new URLSearchParams(params())),
                  ...page(),
                },
              )
            }
          >
            Export page
          </button>
          <button
            class="text-button"
            onClick={() =>
              change(() => {
                setSearch("");
                setQuery("");
                setColumn("");
                setValue("");
                setFrom("");
                setTo("");
                setComments(false);
              })
            }
          >
            Clear filters
          </button>
        </div>
        <Show when={schema()}>
          <pre class="schema">{props.table.sql}</pre>
        </Show>
        <Show when={page.error}>
          <div class="alert error" role="alert">
            {String(page.error?.message || page.error)}{" "}
            <button onClick={() => void refetch()}>Retry</button>
          </div>
        </Show>
        <Show when={page.loading}>
          <div class="loading" role="status">
            Loading records…
          </div>
        </Show>
        <Show when={!page.error && page() && !page.loading}>
          <Show
            when={page()!.rows.length}
            fallback={
              <div class="empty">
                <h2>No records found</h2>
                <p>Try another search or clear the filters.</p>
              </div>
            }
          >
            <div class="table-scroll">
              <table>
                <thead>
                  <tr>
                    <For each={columns()}>{(c) => <th>{label(c)}</th>}</For>
                    <th>
                      <span class="sr-only">Inspect</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <For each={page()!.rows}>
                    {(row) => (
                      <tr>
                        <For each={columns()}>
                          {(c) => (
                            <td title={cell(row, c)}>
                              <Show
                                when={[
                                  "term",
                                  "_term",
                                  "normalized_term",
                                ].includes(c)}
                                fallback={
                                  <span
                                    classList={{
                                      badge: [
                                        "learning_status",
                                        "rating",
                                        "effective_rating",
                                        "selection_kind",
                                        "usefulness",
                                      ].includes(c),
                                    }}
                                    data-value={String(row[c])}
                                  >
                                    {cell(row, c)}
                                  </span>
                                }
                              >
                                <button
                                  class="row-link"
                                  onClick={() => props.open(row)}
                                >
                                  {cell(row, c)}
                                </button>
                              </Show>
                            </td>
                          )}
                        </For>
                        <td>
                          <button
                            class="text-button"
                            onClick={() => props.open(row)}
                            aria-label={`Inspect ${display(row.term || row._term || row.id)}`}
                          >
                            Open
                          </button>
                          <Show when={props.table.name === "vocabulary_items"}>
                            <button
                              class="text-button"
                              onClick={() => props.inspect(row)}
                              aria-label={`Inspect raw ${display(row.term)}`}
                            >
                              Raw
                            </button>
                          </Show>
                        </td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
          </Show>
        </Show>
        <div class="pagination">
          <span>
            {!page.error && page() && !page.loading
              ? `${page()!.total ? page()!.offset + 1 : 0}–${page()!.offset + page()!.rows.length} of ${page()!.total.toLocaleString()} records`
              : "Records"}
            <small>
              Times shown in {Intl.DateTimeFormat().resolvedOptions().timeZone}
            </small>
          </span>
          <div class="toolbar">
            <label>
              Rows
              <select
                value={limit()}
                onChange={(e) =>
                  change(() => setLimit(Number(e.currentTarget.value)))
                }
              >
                <option>25</option>
                <option>50</option>
                <option>100</option>
              </select>
            </label>
            <button
              disabled={offset() === 0 || page.loading}
              onClick={() => setOffset(Math.max(0, offset() - limit()))}
            >
              Previous
            </button>
            <button
              disabled={
                !!page.error ||
                !page() ||
                offset() + limit() >= page()!.total ||
                page.loading
              }
              onClick={() => setOffset(offset() + limit())}
            >
              Next
            </button>
          </div>
        </div>
      </div>
      <Show when={props.table.name !== "vocabulary_items"}>
        <p class="footnote">
          Stored history, cache, scheduling state, and system tables are
          read-only. Vocabulary changes use the MCP’s validation and scheduling
          rules.
        </p>
      </Show>
    </>
  );
}
