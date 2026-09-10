import { createResource, createSignal, For, onCleanup, Show } from "solid-js";
import { APIError, type API } from "../api";
import type { Vocabulary } from "../types";
import { date, pretty } from "../format";
import {
  draftDirty,
  fieldLabels,
  mergeDraft,
  vocabularyChanges,
  vocabularyDraft,
  vocabularyPatch,
  type Draft,
  type DraftConflict,
  type LeaveGuard,
} from "../vocabularyDraft";
import Modal from "./Modal";
import TextList from "./TextList";

export default function VocabularyEditor(props: {
  api: API;
  id?: string;
  close: () => void;
  saved: (message: string) => void;
  history: (id: string) => void;
  registerGuard: (guard: LeaveGuard | undefined) => void;
}) {
  const [busy, setBusy] = createSignal(false);
  const [pendingLeave, setPendingLeave] = createSignal<() => void>();
  let dirty = () => false;
  const requestLeave: LeaveGuard = (leave) => {
    if (busy()) return;
    if (dirty()) setPendingLeave(() => leave);
    else leave();
  };
  props.registerGuard(requestLeave);
  const beforeUnload = (event: BeforeUnloadEvent) => {
    if (!dirty() && !busy()) return;
    event.preventDefault();
    event.returnValue = "";
  };
  window.addEventListener("beforeunload", beforeUnload);
  onCleanup(() => {
    props.registerGuard(undefined);
    window.removeEventListener("beforeunload", beforeUnload);
  });
  const [item, { refetch }] = createResource(
    () => props.id,
    (id) => props.api<Vocabulary>(`/vocabulary/${encodeURIComponent(id)}`),
  );
  const empty: Vocabulary = {
    itemId: "",
    revision: 1,
    term: "",
    normalizedTerm: "",
    status: "new",
    usefulness: "normal",
    personalInterest: "normal",
    tags: [],
    notes: [],
    examples: [],
    createdAt: "",
    updatedAt: "",
  };
  return (
    <Modal
      title={props.id ? "Vocabulary item" : "Add vocabulary"}
      close={() => requestLeave(props.close)}
      wide
      busy={busy()}
    >
      <Show when={item.error}>
        <div class="dialog-body alert error" role="alert">
          {item.error?.message}
          <button onClick={() => void refetch()}>Retry</button>
        </div>
      </Show>
      <Show when={item.loading}>
        <div class="dialog-body" role="status">
          Loading vocabulary…
        </div>
      </Show>
      <Show
        when={!item.error && !item.loading && (props.id ? item() : empty)}
        keyed
      >
        {(loaded) => (
          <Editor
            item={loaded}
            api={props.api}
            busy={busy()}
            setBusy={setBusy}
            registerDirty={(check) => {
              dirty = check;
            }}
            close={() => requestLeave(props.close)}
            saved={props.saved}
            history={props.history}
          />
        )}
      </Show>
      <Show when={pendingLeave()}>
        <Modal
          title="Unsaved vocabulary changes"
          close={() => setPendingLeave(undefined)}
        >
          <div class="dialog-body">
            <p>
              Your draft has not been saved. Keep editing, or discard it to
              continue.
            </p>
          </div>
          <footer class="dialog-footer">
            <button autofocus onClick={() => setPendingLeave(undefined)}>
              Keep editing
            </button>
            <button
              class="danger"
              onClick={() => {
                const leave = pendingLeave();
                setPendingLeave(undefined);
                leave?.();
              }}
            >
              Discard draft and continue
            </button>
          </footer>
        </Modal>
      </Show>
    </Modal>
  );
}

function Editor(props: {
  item: Vocabulary;
  api: API;
  busy: boolean;
  setBusy: (value: boolean) => void;
  registerDirty: (check: () => boolean) => void;
  close: () => void;
  saved: (message: string) => void;
  history: (id: string) => void;
}) {
  const isNew = !props.item.itemId;
  const [item, setItem] = createSignal(props.item);
  const [base, setBase] = createSignal(vocabularyDraft(props.item));
  const [draft, setDraft] = createSignal(base());
  const change = <K extends keyof Draft>(field: K, value: Draft[K]) =>
    setDraft((current) => ({ ...current, [field]: value }));
  const [error, setError] = createSignal("");
  const [stale, setStale] = createSignal(false);
  const [conflicts, setConflicts] = createSignal<DraftConflict[]>([]);
  const [mergeNotice, setMergeNotice] = createSignal("");
  const [confirm, setConfirm] = createSignal(false);
  const [deletion, setDeletion] = createSignal("");
  let active = true;
  props.registerDirty(
    () => draftDirty(base(), draft()) || conflicts().length > 0,
  );
  onCleanup(() => {
    active = false;
    props.registerDirty(() => false);
  });
  const save = async (event: SubmitEvent) => {
    event.preventDefault();
    if (props.busy || stale() || conflicts().length) return;
    if (
      !isNew &&
      Object.keys(vocabularyChanges(base(), draft())).length === 0
    ) {
      // A successful no-op must not reopen the dirty guard for blank list rows.
      props.saved("No metadata changes to save.");
      return;
    }
    props.setBusy(true);
    setError("");
    try {
      const body = isNew
        ? vocabularyChanges(base(), draft(), true)
        : vocabularyPatch(base(), draft(), item().revision);
      const result = await props.api<Vocabulary & { created?: boolean }>(
        `/vocabulary${isNew ? "" : `/${encodeURIComponent(item().itemId)}`}`,
        { method: isNew ? "POST" : "PATCH", body: JSON.stringify(body) },
      );
      if (!active) return;
      props.saved(
        isNew && result.created === false
          ? "This meaning already exists. Existing data was kept."
          : isNew
            ? "Vocabulary added."
            : "Vocabulary updated.",
      );
    } catch (error) {
      if (!active) return;
      if (!isNew && error instanceof APIError && error.status === 409) {
        setStale(true);
        setMergeNotice("");
      }
      setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (active) props.setBusy(false);
    }
  };
  const reload = async (discard: boolean) => {
    if (props.busy) return;
    props.setBusy(true);
    setError("");
    try {
      const latest = await props.api<Vocabulary>(
        `/vocabulary/${encodeURIComponent(item().itemId)}`,
      );
      if (!active) return;
      const remote = vocabularyDraft(latest);
      const merged = discard
        ? { draft: remote, conflicts: [] }
        : mergeDraft(base(), draft(), remote);
      setItem(latest);
      setBase(remote);
      setDraft(merged.draft);
      setConflicts(merged.conflicts);
      setStale(false);
      setMergeNotice(
        discard
          ? "Latest version loaded. Your previous draft was discarded."
          : "Latest version loaded. Local-only edits were kept and untouched fields updated. Review the merged draft before saving.",
      );
    } catch (error) {
      if (active)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (active) props.setBusy(false);
    }
  };
  const resolve = (conflict: DraftConflict, useRemote: boolean) => {
    if (useRemote) change(conflict.field, conflict.remote);
    setConflicts((current) =>
      current.filter((entry) => entry.field !== conflict.field),
    );
  };
  const remove = async () => {
    if (props.busy || deletion() !== item().term) return;
    props.setBusy(true);
    setError("");
    try {
      await props.api(`/vocabulary/${encodeURIComponent(item().itemId)}`, {
        method: "DELETE",
      });
      if (active) props.saved("Vocabulary deleted. Review history retained.");
    } catch (error) {
      if (active)
        setError(error instanceof Error ? error.message : String(error));
    } finally {
      if (active) props.setBusy(false);
    }
  };
  return (
    <form onSubmit={save}>
      <fieldset disabled={props.busy} class="dialog-body editor-fields">
        <Show when={!isNew}>
          <div class="item-heading">
            <div>
              <h3>{item().term}</h3>
              <code>{item().itemId}</code>
              <Show when={item().context}>
                <p class="muted">Context / meaning: {item().context}</p>
              </Show>
            </div>
            <button type="button" onClick={() => props.history(item().itemId)}>
              Review history
            </button>
          </div>
        </Show>
        <Show when={error()}>
          <div class="alert error" role="alert">
            {error()}
          </div>
        </Show>
        <Show when={stale()}>
          <div class="alert" role="alert">
            <p>
              This item changed after you opened it. Your draft is intact. Load
              the latest version and merge, or explicitly discard your draft.
              Saving is paused until you review the changes.
            </p>
            <div class="toolbar">
              <button type="button" onClick={() => void reload(false)}>
                Load latest and merge draft
              </button>
              <button type="button" onClick={() => void reload(true)}>
                Discard draft and load latest
              </button>
            </div>
          </div>
        </Show>
        <Show when={mergeNotice()}>
          <p role="status">{mergeNotice()}</p>
        </Show>
        <For each={conflicts()}>
          {(conflict) => (
            <section
              class="alert"
              aria-label={`${fieldLabels[conflict.field]} conflict`}
            >
              <strong>{fieldLabels[conflict.field]} needs your decision</strong>
              <div>
                Your draft: <pre>{pretty(draft()[conflict.field])}</pre>
              </div>
              <div>
                Latest saved value:{" "}
                <pre>
                  {conflict.field === "usefulness"
                    ? `Existing hint is not exposed; computed usefulness is ${item().usefulness}.`
                    : pretty(conflict.remote)}
                </pre>
              </div>
              <div class="toolbar">
                <button type="button" onClick={() => resolve(conflict, false)}>
                  Use my {fieldLabels[conflict.field].toLowerCase()}
                </button>
                <button type="button" onClick={() => resolve(conflict, true)}>
                  Keep latest {fieldLabels[conflict.field].toLowerCase()}
                </button>
              </div>
            </section>
          )}
        </For>
        <Show when={isNew}>
          <label>
            Word or expression
            <input
              required
              maxlength="200"
              autofocus
              value={draft().term}
              onInput={(e) => change("term", e.currentTarget.value)}
              placeholder="e.g. put someone through the wringer"
            />
          </label>
          <label>
            Context / meaning
            <input
              value={draft().context}
              onInput={(e) => change("context", e.currentTarget.value)}
              placeholder="Optional: distinguish this meaning from another"
            />
          </label>
        </Show>
        <div class="form-grid">
          <label>
            Learning status
            <select
              value={draft().status}
              onChange={(e) => change("status", e.currentTarget.value)}
            >
              <option value="new">New</option>
              <option value="learning">Learning</option>
              <option value="learned">Learned</option>
              <option value="archived">Archived</option>
            </select>
          </label>
          <label>
            Usefulness hint
            <select
              value={draft().usefulness}
              onChange={(e) => change("usefulness", e.currentTarget.value)}
            >
              <option value="">
                {isNew ? "Automatic" : "Keep existing hint"}
              </option>
              <option value="low">Low</option>
              <option value="normal">Normal</option>
              <option value="high">High</option>
            </select>
            <small>
              Combined with offline evidence. Current result:{" "}
              {item().usefulness}.
            </small>
          </label>
          <label>
            Personal interest
            <select
              value={draft().personalInterest}
              onChange={(e) =>
                change(
                  "personalInterest",
                  e.currentTarget.value as Vocabulary["personalInterest"],
                )
              }
            >
              <option value="low">Low</option>
              <option value="normal">Normal</option>
              <option value="high">High</option>
            </select>
            <small>
              Your personal priority, independent of general usefulness. High
              favors learning sooner; low reduces chance without excluding the
              word.
            </small>
          </label>
        </div>
        <label>
          Description
          <textarea
            rows="3"
            value={draft().customDescription}
            onInput={(e) => change("customDescription", e.currentTarget.value)}
          />
        </label>
        <TextList
          label="Notes"
          values={draft().notes}
          change={(values) => change("notes", values)}
        />
        <TextList
          label="Examples"
          values={draft().examples}
          change={(values) => change("examples", values)}
        />
        <TextList
          label="Tags"
          values={draft().tags}
          change={(values) => change("tags", values)}
        />
        <details>
          <summary>Description source</summary>
          <div class="form-grid">
            <label>
              Title
              <input
                value={draft().sourceTitle}
                onInput={(e) => change("sourceTitle", e.currentTarget.value)}
              />
            </label>
            <label>
              URL
              <input
                type="url"
                value={draft().sourceURL}
                onInput={(e) => change("sourceURL", e.currentTarget.value)}
              />
            </label>
          </div>
        </details>
        <Show when={!isNew}>
          <details>
            <summary>Stored sense, dictionary, and metadata</summary>
            <p class="muted">
              Created {date(item().createdAt)} · Updated{" "}
              {date(item().updatedAt)} · Revision {item().revision}
            </p>
            <pre>{pretty(item())}</pre>
          </details>
        </Show>
        <Show when={confirm()}>
          <div class="danger-zone">
            <strong>Delete “{item().term}”?</strong>
            <p>
              This removes the vocabulary item and its learning cards.
              Historical reviews and presentations remain. Archive the item to
              preserve its cards. Any unsaved draft will be discarded.
            </p>
            <label>
              Type the exact term to confirm
              <input
                value={deletion()}
                onInput={(e) => setDeletion(e.currentTarget.value)}
                autocomplete="off"
              />
            </label>
            <div class="toolbar">
              <button
                type="button"
                class="danger"
                disabled={deletion() !== item().term}
                onClick={() => void remove()}
              >
                Delete permanently
              </button>
              <button type="button" onClick={() => setConfirm(false)}>
                Keep item
              </button>
            </div>
          </div>
        </Show>
      </fieldset>
      <footer class="dialog-footer">
        <Show when={!isNew}>
          <button
            type="button"
            class="text-button danger-text"
            disabled={props.busy}
            onClick={() => setConfirm(true)}
          >
            Delete item
          </button>
        </Show>
        <span class="spacer" />
        <button type="button" disabled={props.busy} onClick={props.close}>
          Cancel
        </button>
        <button
          class="primary"
          type="submit"
          disabled={props.busy || stale() || conflicts().length > 0}
        >
          {props.busy ? "Working…" : isNew ? "Add word" : "Save changes"}
        </button>
      </footer>
    </form>
  );
}
