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
import {
  dateFilterColumn,
  filterOptions,
  quickFilterColumn,
} from "../tableFilters";
import DateRangePicker from "./DateRangePicker";

const preferred: Record<string, string[]> = {
  vocabulary_items: [
    "term",
    "learning_status",
    "personal_interest",
    "usefulness",
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
    preferred[props.table.name]?.filter((c) => allColumns().includes(c)) ||
      allColumns().slice(0, 6),
  );
  const [search, setSearch] = createSignal("");
  const [query, setQuery] = createSignal("");
  const [column, setColumn] = createSignal(props.initialFilter?.column || "");
  const [value, setValue] = createSignal(props.initialFilter?.value || "");
  const [comments, setComments] = createSignal(false);
  const [sort, setSort] = createSignal(
    defaultSort[props.table.name] || props.table.columns[0].name,
  );
  const [direction, setDirection] = createSignal(
    props.table.name === "learning_cards" ? "asc" : "desc",
  );
  const [from, setFrom] = createSignal("");
  const [to, setTo] = createSignal("");
  const [limit, setLimit] = createSignal(50);
  const [offset, setOffset] = createSignal(0);
  const [schema, setSchema] = createSignal(false);
  const [advanced, setAdvanced] = createSignal(false);
  const [draftColumn, setDraftColumn] = createSignal(
    props.initialFilter?.column || "",
  );
  const [draftValue, setDraftValue] = createSignal(
    props.initialFilter?.value || "",
  );
  const [compact, setCompact] = createSignal(false);
  const quickColumn = () => quickFilterColumn(props.table.name);
  const filtersActive = () =>
    !!(query() || column() || from() || to() || comments());
  const clearFilters = () =>
    change(() => {
      setSearch("");
      setQuery("");
      setColumn("");
      setValue("");
      setDraftColumn("");
      setDraftValue("");
      setFrom("");
      setTo("");
      setComments(false);
    });
  const setExact = (nextColumn: string, nextValue: string) =>
    change(() => {
      setColumn(nextColumn);
      setValue(nextValue);
      setDraftColumn(nextColumn);
      setDraftValue(nextValue);
    });
  const sortBy = (key: string) =>
    change(() => {
      setDirection(sort() === key && direction() === "asc" ? "desc" : "asc");
      setSort(key);
    });
  const params = createMemo(() => {
    const p = new URLSearchParams({
      q: query(),
      column: column(),
      value: value(),
      sort: sort(),
      direction: direction(),
      from: from(),
      to: to(),
      comments: String(props.commentsOnly || comments()),
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
  const dateFilter = () => dateFilterColumn(tableColumns());
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
          <span class="eyebrow">
            {props.table.name === "vocabulary_items"
              ? "Your collection"
              : "Explore records"}
          </span>
          <h1>{props.commentsOnly ? "Comments" : label(props.table.name)}</h1>
          <p>
            {props.commentsOnly
              ? "Tutor comments and ratings for reviews with comments. Answer text is not stored."
              : props.table.name === "vocabulary_items"
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
        <div class="table-tools primary-tools">
          <form
            class="search"
            onSubmit={(e) => {
              e.preventDefault();
              change(() => setQuery(search().trim()));
            }}
          >
            <input
              type="search"
              aria-label="Search all fields or word"
              placeholder="Search words or any stored field…"
              maxlength="500"
              value={search()}
              onInput={(e) => setSearch(e.currentTarget.value)}
            />
            <button type="submit">Search</button>
          </form>
          <Show when={dateFilter()}>
            {(field) => (
              <DateRangePicker
                field={label(field()).replace(/ at$/, "")}
                value={{ from: from(), to: to() }}
                change={(range) =>
                  change(() => {
                    setFrom(range.from);
                    setTo(range.to);
                  })
                }
              />
            )}
          </Show>
          <button
            classList={{ selected: advanced() }}
            aria-expanded={advanced()}
            aria-controls="advanced-filters"
            onClick={() => {
              setDraftColumn(column());
              setDraftValue(value());
              setAdvanced(!advanced());
            }}
          >
            Filters & tools{" "}
            <span aria-hidden="true">{advanced() ? "−" : "+"}</span>
          </button>
        </div>
        <div class="table-tools quick-tools">
          <Show when={quickColumn()}>
            <div
              class="filter-pills"
              aria-label={
                quickColumn() === "rating" ? "Rating filters" : "Status filters"
              }
            >
              <button
                aria-pressed={column() !== quickColumn()}
                onClick={() => {
                  if (column() === quickColumn()) setExact("", "");
                }}
              >
                {quickColumn() === "rating" ? "All ratings" : "All statuses"}
              </button>
              <For each={filterOptions(quickColumn())}>
                {(option) => (
                  <button
                    aria-pressed={
                      column() === quickColumn() && value() === option
                    }
                    onClick={() => setExact(quickColumn(), option)}
                  >
                    {label(option)}
                  </button>
                )}
              </For>
            </div>
          </Show>
          <Show
            when={props.table.name === "review_attempts" && !props.commentsOnly}
          >
            <label class="check">
              <input
                type="checkbox"
                checked={comments()}
                onChange={(e) =>
                  change(() => setComments(e.currentTarget.checked))
                }
              />
              With comments
            </label>
          </Show>
          <span class="spacer" />
          <span class="scope-label">
            {column() === "owner_key" ? `Owner: ${value()}` : "All owners"}
          </span>
          <button
            class="density-toggle"
            aria-pressed={compact()}
            onClick={() => setCompact(!compact())}
          >
            Compact rows
          </button>
        </div>
        <Show when={advanced()}>
          <div class="advanced-filters" id="advanced-filters">
            <form
              class="exact-filter"
              onSubmit={(e) => {
                e.preventDefault();
                setExact(draftColumn(), draftColumn() ? draftValue() : "");
              }}
            >
              <label>
                Match field
                <select
                  value={draftColumn()}
                  onChange={(e) => {
                    setDraftColumn(e.currentTarget.value);
                    setDraftValue(
                      filterOptions(e.currentTarget.value)[0] || "",
                    );
                  }}
                >
                  <option value="">No field filter</option>
                  <For each={tableColumns()}>
                    {(c) => <option value={c}>{label(c)}</option>}
                  </For>
                </select>
              </label>
              <Show when={draftColumn()}>
                <label>
                  Exact value
                  <Show
                    when={filterOptions(draftColumn()).length}
                    fallback={
                      <input
                        maxlength="500"
                        value={draftValue()}
                        onInput={(e) => setDraftValue(e.currentTarget.value)}
                        placeholder="Value to match"
                      />
                    }
                  >
                    <select
                      value={draftValue()}
                      onChange={(e) => setDraftValue(e.currentTarget.value)}
                    >
                      <Show
                        when={
                          !filterOptions(draftColumn()).includes(draftValue())
                        }
                      >
                        <option value={draftValue()}>
                          {draftValue() || "Choose a value"}
                        </option>
                      </Show>
                      <For each={filterOptions(draftColumn())}>
                        {(option) => (
                          <option value={option}>{label(option)}</option>
                        )}
                      </For>
                    </select>
                  </Show>
                </label>
              </Show>
              <button type="submit">Apply filter</button>
              <small>
                One exact-field filter at a time; applying one replaces the
                current field filter, including a quick status or rating filter.
              </small>
            </form>
            <div class="table-tools secondary-tools">
              <label>
                Sort by
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
                          disabled={
                            columns().length === 1 && columns().includes(c)
                          }
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
              <button
                aria-expanded={schema()}
                onClick={() => setSchema(!schema())}
              >
                {schema() ? "Hide schema" : "Schema"}
              </button>
              <button
                disabled={!!page.error || !page() || page.loading}
                onClick={() =>
                  download(
                    `${props.table.name}-page-${offset() / limit() + 1}.json`,
                    {
                      table: props.table.name,
                      filters: Object.fromEntries(
                        new URLSearchParams(params()),
                      ),
                      ...page(),
                    },
                  )
                }
              >
                Export page
              </button>
            </div>
          </div>
        </Show>
        <Show when={filtersActive()}>
          <div class="active-filters" aria-label="Active filters">
            <Show when={query()}>
              <button
                aria-label="Remove search filter"
                onClick={() =>
                  change(() => {
                    setQuery("");
                    setSearch("");
                  })
                }
              >
                Search: {query()} <span aria-hidden="true">×</span>
              </button>
            </Show>
            <Show when={column()}>
              <button
                aria-label="Remove field filter"
                onClick={() => setExact("", "")}
              >
                {label(column())}: {value() || "(empty)"}{" "}
                <span aria-hidden="true">×</span>
              </button>
            </Show>
            <Show when={from() || to()}>
              <button
                aria-label="Remove date filter"
                onClick={() =>
                  change(() => {
                    setFrom("");
                    setTo("");
                  })
                }
              >
                {label(dateFilter() || "Date")}: {from() || "Any"} →{" "}
                {to() || "Any"} · UTC <span aria-hidden="true">×</span>
              </button>
            </Show>
            <Show when={comments()}>
              <button onClick={() => change(() => setComments(false))}>
                With comments <span aria-hidden="true">×</span>
              </button>
            </Show>
            <button class="text-button" onClick={clearFilters}>
              Clear filters
            </button>
          </div>
        </Show>
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
            {page.latest ? "Updating records…" : "Loading records…"}
          </div>
        </Show>
        <Show when={!page.error && page.latest}>
          <Show
            when={page.latest!.rows.length}
            fallback={
              <div class="empty">
                <h2>No records found</h2>
                <p>
                  {filtersActive()
                    ? "Try a broader search or remove a filter."
                    : props.table.name === "vocabulary_items"
                      ? "Add a word or expression to start your collection."
                      : "Records will appear here as you use your connected tutor."}
                </p>
                <Show
                  when={filtersActive()}
                  fallback={
                    <Show when={props.table.name === "vocabulary_items"}>
                      <button class="primary" onClick={props.create}>
                        + Add word
                      </button>
                    </Show>
                  }
                >
                  <button onClick={clearFilters}>Clear filters</button>
                </Show>
              </div>
            }
          >
            <div
              classList={{
                "table-scroll": true,
                "records-scroll": true,
                "is-updating": page.loading,
                compact: compact(),
              }}
              tabIndex={0}
              aria-label="Records; scroll for more rows and columns"
              aria-busy={page.loading}
            >
              <table>
                <thead>
                  <tr>
                    <For each={columns()}>
                      {(c) => (
                        <th
                          scope="col"
                          aria-sort={
                            sort() === c
                              ? direction() === "asc"
                                ? "ascending"
                                : "descending"
                              : "none"
                          }
                        >
                          <Show
                            when={tableColumns().includes(c)}
                            fallback={label(c)}
                          >
                            <button
                              class="sort-heading"
                              onClick={() => sortBy(c)}
                            >
                              {label(c)}
                              <span aria-hidden="true">
                                {sort() === c
                                  ? direction() === "asc"
                                    ? "↑"
                                    : "↓"
                                  : "↕"}
                              </span>
                            </button>
                          </Show>
                        </th>
                      )}
                    </For>
                    <th>
                      <span class="sr-only">Inspect</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <For each={page.latest!.rows}>
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
                                        "personal_interest",
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
                                  disabled={page.loading}
                                  onClick={() => props.open(row)}
                                >
                                  {cell(row, c)}
                                </button>
                                <Show
                                  when={
                                    c === "term" &&
                                    row.context &&
                                    !columns().includes("context")
                                  }
                                >
                                  <small class="term-context">
                                    {display(row.context)}
                                  </small>
                                </Show>
                              </Show>
                            </td>
                          )}
                        </For>
                        <td>
                          <button
                            class="text-button"
                            disabled={page.loading}
                            onClick={() => props.open(row)}
                            aria-label={`Inspect ${display(row.term || row._term || row.id)}`}
                          >
                            Open
                          </button>
                          <Show when={props.table.name === "vocabulary_items"}>
                            <button
                              class="text-button"
                              disabled={page.loading}
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
