import {
  createMemo,
  createResource,
  createSignal,
  ErrorBoundary,
  For,
  onCleanup,
  onMount,
  Show,
} from "solid-js";
import { APIError, createAPI } from "./api";
import { createAuth } from "./auth";
import type { Row, Session, Table } from "./types";
import { label } from "./format";
import Analytics from "./components/Analytics";
import Inspector from "./components/Inspector";
import RecordTable from "./components/RecordTable";
import VocabularyEditor from "./components/VocabularyEditor";

type Route = { view: string; column?: string; value?: string };
function readRoute(): Route {
  const p = new URLSearchParams(location.hash.slice(1));
  return {
    view: p.get("view") || "vocabulary_items",
    column: p.get("column") || undefined,
    value: p.get("value") || undefined,
  };
}

export default function App() {
  const auth = createAuth();
  const [session, setSession] = createSignal<Session>();
  const [token, setToken] = createSignal("");
  const [loginToken, setLoginToken] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [loginError, setLoginError] = createSignal("");
  const [remember, setRemember] = createSignal(false);
  const [authNotice, setAuthNotice] = createSignal("");
  let generation = 0;
  onCleanup(() => {
    generation++;
  });
  const signOut = () => {
    generation++;
    const forgotten = auth.forget();
    setSession(undefined);
    setToken("");
    setLoginToken("");
    setRemember(false);
    setBusy(false);
    setAuthNotice("");
    setLoginError(
      forgotten
        ? ""
        : "Signed out. Your browser could not clear the saved token; clear this site's stored data to remove it.",
    );
  };
  const signIn = async (candidate: string) => {
    const attempt = ++generation;
    setBusy(true);
    setLoginError("");
    try {
      const connected = await auth.signIn(candidate, remember());
      if (attempt !== generation) return;
      setToken(connected.token);
      setAuthNotice(
        connected.storageAvailable
          ? ""
          : "Signed in for this tab. Your browser could not update the saved sign-in preference.",
      );
      setSession(connected.session);
      setLoginToken("");
    } catch (e) {
      if (attempt !== generation) return;
      if (e instanceof APIError && e.status === 401) {
        setLoginToken("");
        setRemember(false);
      }
      setLoginError(e instanceof Error ? e.message : String(e));
    } finally {
      if (attempt === generation) setBusy(false);
    }
  };
  const login = (e: SubmitEvent) => {
    e.preventDefault();
    if (!busy()) void signIn(loginToken());
  };
  onMount(() => {
    const saved = auth.rememberedToken();
    if (!saved) return;
    setRemember(true);
    setLoginToken(saved);
    void signIn(saved);
  });
  return (
    <Show
      when={session()}
      keyed
      fallback={
        <main class="login-page">
          <section class="login-card">
            <div class="brand-mark">En</div>
            <h1>English MCP</h1>
            <p>Sign in to your admin workspace.</p>
            <form onSubmit={login}>
              <label>
                Admin token
                <input
                  type="password"
                  autocomplete="current-password"
                  autofocus
                  required
                  disabled={busy()}
                  value={loginToken()}
                  onInput={(e) => setLoginToken(e.currentTarget.value)}
                  placeholder="Enter your admin token"
                />
              </label>
              <label class="check">
                <input
                  type="checkbox"
                  checked={remember()}
                  disabled={busy()}
                  onChange={(e) => setRemember(e.currentTarget.checked)}
                />
                Remember me on this device
              </label>
              <Show when={loginError()}>
                <div class="alert error" role="alert">
                  {loginError()}
                </div>
              </Show>
              <button class="primary" type="submit" disabled={busy()}>
                {busy() ? "Connecting…" : "Sign in"}
              </button>
            </form>
            <p class="footnote">
              Use ADMIN_BEARER_TOKEN from your MCP service. Remember me saves
              the token in this browser until you sign out. Leave it unchecked
              for this tab only.
            </p>
          </section>
        </main>
      }
    >
      {(active) => {
        const activeToken = token();
        return (
          <Workspace
            session={active}
            authNotice={authNotice()}
            api={createAPI(activeToken, () => {
              if (token() !== activeToken) return;
              signOut();
              setLoginError(
                "Your session is no longer authorized. Sign in again.",
              );
            })}
            signOut={signOut}
          />
        );
      }}
    </Show>
  );
}

function Workspace(props: {
  session: Session;
  api: ReturnType<typeof createAPI>;
  signOut: () => void;
  authNotice: string;
}) {
  const [route, setRoute] = createSignal(readRoute());
  const onHash = () => setRoute(readRoute());
  window.addEventListener("hashchange", onHash);
  onCleanup(() => window.removeEventListener("hashchange", onHash));
  const [revision, setRevision] = createSignal(0);
  const [tables, { refetch }] = createResource(revision, () =>
    props.api<Table[]>("/tables"),
  );
  const [editor, setEditor] = createSignal<{ id?: string; hint?: string }>();
  const [inspected, setInspected] = createSignal<{ row: Row; table: string }>();
  const [notice, setNotice] = createSignal("");
  const [exporting, setExporting] = createSignal(false);
  const [exportError, setExportError] = createSignal("");
  const exportDatabase = async () => {
    setExporting(true);
    setExportError("");
    try {
      const blob = await props.api<Blob>(
        "/database",
        { signal: AbortSignal.timeout(120000) },
        "blob",
      );
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `english-mcp-${new Date().toISOString().slice(0, 10)}.sqlite`;
      anchor.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e) {
      setExportError(e instanceof Error ? e.message : String(e));
    } finally {
      setExporting(false);
    }
  };
  let noticeTimer: ReturnType<typeof setTimeout> | undefined;
  onCleanup(() => clearTimeout(noticeTimer));
  const navigate = (view: string, column?: string, value?: string) => {
    setEditor(undefined);
    setInspected(undefined);
    const p = new URLSearchParams({ view });
    if (column) {
      p.set("column", column);
      p.set("value", value || "");
    }
    location.hash = p.toString();
    setRoute({ view, column, value });
  };
  const allTables = () => (tables.error ? [] : tables() || []);
  const tableName = () =>
    route().view === "comments" ? "review_attempts" : route().view;
  const activeTable = createMemo(() =>
    allTables().find((t) => t.name === tableName()),
  );
  const rowOpen = (row: Row) => {
    if (
      tableName() === "vocabulary_items" &&
      row.owner_key === props.session.owner
    )
      setEditor({
        id: String(row.id),
        hint: row.usefulness_hint ? String(row.usefulness_hint) : undefined,
      });
    else setInspected({ row, table: tableName() });
  };
  const saved = (message: string) => {
    setEditor(undefined);
    setRevision((x) => x + 1);
    setNotice(message);
    clearTimeout(noticeTimer);
    noticeTimer = setTimeout(() => setNotice(""), 6000);
  };
  const nav = [
    { name: "Vocabulary", view: "vocabulary_items", icon: "Aa" },
    { name: "Reviews", view: "review_attempts", icon: "↺" },
    { name: "Comments", view: "comments", icon: "“" },
    { name: "Presentations", view: "learning_presentations", icon: "▤" },
    { name: "Analytics", view: "analytics", icon: "▥" },
  ];
  return (
    <div class="app-shell">
      <a class="skip-link" href="#main">
        Skip to content
      </a>
      <aside class="sidebar">
        <div class="brand">
          <span class="brand-mark">En</span>
          <div>
            <strong>English MCP</strong>
            <small>Admin workspace</small>
          </div>
        </div>
        <nav aria-label="Main navigation">
          <For each={nav}>
            {(n) => (
              <button
                classList={{ active: route().view === n.view }}
                onClick={() => navigate(n.view)}
                aria-current={route().view === n.view ? "page" : undefined}
              >
                <span class="nav-icon" aria-hidden="true">
                  {n.icon}
                </span>
                {n.name}
              </button>
            )}
          </For>
        </nav>
        <div class="nav-section">Database</div>
        <nav aria-label="Database tables">
          <For
            each={allTables().filter(
              (t) =>
                ![
                  "vocabulary_items",
                  "review_attempts",
                  "learning_presentations",
                ].includes(t.name),
            )}
          >
            {(t) => (
              <button
                classList={{ active: route().view === t.name }}
                onClick={() => navigate(t.name)}
                aria-current={route().view === t.name ? "page" : undefined}
              >
                {label(t.name)}
                <small>{t.count.toLocaleString()}</small>
              </button>
            )}
          </For>
        </nav>
        <div class="sidebar-bottom">
          <button disabled={exporting()} onClick={() => void exportDatabase()}>
            {exporting() ? "Exporting…" : "Export database"}
          </button>
          <small>Owner</small>
          <code>{props.session.owner}</code>
          <button onClick={props.signOut}>Sign out</button>
        </div>
      </aside>
      <main id="main" class="main-content">
        <header class="topbar">
          <span>
            English learning /{" "}
            <strong>
              {route().view === "analytics" ? "Analytics" : label(tableName())}
            </strong>
          </span>
          <span class="badge">Private admin</span>
        </header>
        <div class="workspace">
          <Show when={props.authNotice}>
            <div class="alert" role="status">
              {props.authNotice}
            </div>
          </Show>
          <Show when={exportError()}>
            <div class="alert error" role="alert">
              {exportError()}
              <button onClick={() => void exportDatabase()}>
                Retry export
              </button>
            </div>
          </Show>
          <Show when={notice()}>
            <div class="alert success" role="status">
              {notice()}
              <button
                class="icon-button"
                aria-label="Dismiss notification"
                onClick={() => setNotice("")}
              >
                ×
              </button>
            </div>
          </Show>
          <Show when={tables.error}>
            <div class="alert error" role="alert">
              {tables.error?.message}
              <button onClick={() => void refetch()}>Retry</button>
            </div>
          </Show>
          <Show when={tables.loading && !allTables().length}>
            <div class="loading" role="status">
              Loading database…
            </div>
          </Show>
          <ErrorBoundary
            fallback={(error, reset) => (
              <div class="alert error" role="alert">
                {String(error.message || error)}
                <button onClick={reset}>Try again</button>
              </div>
            )}
          >
            <Show
              when={route().view === "analytics"}
              fallback={
                <Show when={JSON.stringify(route())} keyed>
                  {(_route) => (
                    <Show
                      when={activeTable()}
                      keyed
                      fallback={
                        <Show when={!tables.loading && !tables.error}>
                          <div class="empty">
                            Select a database table from the sidebar.
                          </div>
                        </Show>
                      }
                    >
                      {(table) => (
                        <RecordTable
                          api={props.api}
                          table={table}
                          revision={revision()}
                          owner={props.session.owner}
                          initialFilter={
                            route().column
                              ? {
                                  column: route().column!,
                                  value: route().value || "",
                                }
                              : undefined
                          }
                          commentsOnly={route().view === "comments"}
                          open={rowOpen}
                          inspect={(row) =>
                            setInspected({ row, table: tableName() })
                          }
                          create={() => setEditor({})}
                        />
                      )}
                    </Show>
                  )}
                </Show>
              }
            >
              <Analytics
                api={props.api}
                revision={revision()}
                open={(id) => setEditor({ id })}
              />
            </Show>
          </ErrorBoundary>
        </div>
      </main>
      <Show when={editor()} keyed>
        {(value) => (
          <VocabularyEditor
            api={props.api}
            id={value.id}
            hint={value.hint}
            close={() => setEditor(undefined)}
            saved={saved}
            history={(id) =>
              navigate("review_attempts", "vocabulary_item_id", id)
            }
          />
        )}
      </Show>
      <Show when={inspected()} keyed>
        {(value) => (
          <Inspector
            row={value.row}
            table={value.table}
            close={() => setInspected(undefined)}
            related={navigate}
            edit={(id) => {
              setInspected(undefined);
              setEditor({ id });
            }}
          />
        )}
      </Show>
    </div>
  );
}
