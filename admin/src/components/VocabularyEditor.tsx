import { createResource, createSignal, Show } from "solid-js";
import type { API } from "../api";
import type { Vocabulary } from "../types";
import { date, pretty } from "../format";
import Modal from "./Modal";
import TextList from "./TextList";

export default function VocabularyEditor(props: {
  api: API;
  id?: string;
  hint?: string;
  close: () => void;
  saved: (message: string) => void;
  history: (id: string) => void;
}) {
  const [item, { refetch }] = createResource(
    () => props.id,
    (id) => props.api<Vocabulary>(`/vocabulary/${encodeURIComponent(id)}`),
  );
  const empty: Vocabulary = {
    itemId: "",
    term: "",
    normalizedTerm: "",
    status: "new",
    usefulness: "normal",
    tags: [],
    notes: [],
    examples: [],
    createdAt: "",
    updatedAt: "",
  };
  return (
    <Modal
      title={props.id ? "Vocabulary item" : "Add vocabulary"}
      close={props.close}
      wide
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
            hint={props.hint}
            api={props.api}
            close={props.close}
            saved={props.saved}
            history={props.history}
          />
        )}
      </Show>
    </Modal>
  );
}

function Editor(props: {
  item: Vocabulary;
  hint?: string;
  api: API;
  close: () => void;
  saved: (message: string) => void;
  history: (id: string) => void;
}) {
  const initial = props.item;
  const isNew = !initial.itemId;
  const [term, setTerm] = createSignal(initial.term);
  const [context, setContext] = createSignal("");
  const [description, setDescription] = createSignal(
    initial.customDescription || "",
  );
  const [status, setStatus] = createSignal(initial.status);
  const [hint, setHint] = createSignal(props.hint || "");
  const [notes, setNotes] = createSignal(initial.notes);
  const [examples, setExamples] = createSignal(initial.examples);
  const [tags, setTags] = createSignal(initial.tags.join(", "));
  const [sourceTitle, setSourceTitle] = createSignal(
    initial.descriptionSource?.title || "",
  );
  const [sourceURL, setSourceURL] = createSignal(
    initial.descriptionSource?.url || "",
  );
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const [confirm, setConfirm] = createSignal(false);
  const [deletion, setDeletion] = createSignal("");
  const close = () => {
    if (!busy()) props.close();
  };
  const save = async (e: SubmitEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    const body = {
      status: status(),
      customDescription: description(),
      tags: tags()
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean),
      notes: notes().filter((x) => x.trim()),
      examples: examples().filter((x) => x.trim()),
      descriptionSource: { title: sourceTitle(), url: sourceURL() },
      ...(hint() ? { usefulness: hint() } : {}),
      ...(isNew ? { term: term(), context: context() } : {}),
    };
    try {
      const result = await props.api<Vocabulary & { created?: boolean }>(
        `/vocabulary${isNew ? "" : `/${encodeURIComponent(initial.itemId)}`}`,
        { method: isNew ? "POST" : "PATCH", body: JSON.stringify(body) },
      );
      props.saved(
        isNew && result.created === false
          ? "This meaning already exists. Existing data was kept."
          : isNew
            ? "Vocabulary added."
            : "Vocabulary updated.",
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    if (deletion() !== initial.term) return;
    setBusy(true);
    setError("");
    try {
      await props.api(`/vocabulary/${encodeURIComponent(initial.itemId)}`, {
        method: "DELETE",
      });
      props.saved("Vocabulary deleted. Review history retained.");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <form onSubmit={save}>
      <fieldset disabled={busy()} class="dialog-body editor-fields">
        <Show when={!isNew}>
          <div class="item-heading">
            <div>
              <h3>{initial.term}</h3>
              <code>{initial.itemId}</code>
            </div>
            <button type="button" onClick={() => props.history(initial.itemId)}>
              Review history
            </button>
          </div>
        </Show>
        <Show when={error()}>
          <div class="alert error" role="alert">
            {error()}
          </div>
        </Show>
        <Show when={isNew}>
          <label>
            Word or expression
            <input
              required
              maxlength="200"
              autofocus
              value={term()}
              onInput={(e) => setTerm(e.currentTarget.value)}
              placeholder="e.g. put someone through the wringer"
            />
          </label>
          <label>
            Context / meaning
            <input
              value={context()}
              onInput={(e) => setContext(e.currentTarget.value)}
              placeholder="Optional: distinguish this meaning from another"
            />
          </label>
        </Show>
        <div class="form-grid">
          <label>
            Learning status
            <select
              value={status()}
              onChange={(e) => setStatus(e.currentTarget.value)}
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
              value={hint()}
              onChange={(e) => setHint(e.currentTarget.value)}
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
              {initial.usefulness}.
            </small>
          </label>
        </div>
        <label>
          Description
          <textarea
            rows="3"
            value={description()}
            onInput={(e) => setDescription(e.currentTarget.value)}
          />
        </label>
        <TextList label="Notes" values={notes()} change={setNotes} />
        <TextList label="Examples" values={examples()} change={setExamples} />
        <label>
          Tags <small>Separated by commas</small>
          <input
            value={tags()}
            onInput={(e) => setTags(e.currentTarget.value)}
            placeholder="idiom, daily-life"
          />
        </label>
        <details>
          <summary>Description source</summary>
          <div class="form-grid">
            <label>
              Title
              <input
                value={sourceTitle()}
                onInput={(e) => setSourceTitle(e.currentTarget.value)}
              />
            </label>
            <label>
              URL
              <input
                type="url"
                value={sourceURL()}
                onInput={(e) => setSourceURL(e.currentTarget.value)}
              />
            </label>
          </div>
        </details>
        <Show when={!isNew}>
          <details>
            <summary>Stored sense, dictionary, and metadata</summary>
            <p class="muted">
              Created {date(initial.createdAt)} · Updated{" "}
              {date(initial.updatedAt)}
            </p>
            <pre>{pretty(initial)}</pre>
          </details>
        </Show>
        <Show when={confirm()}>
          <div class="danger-zone">
            <strong>Delete “{initial.term}”?</strong>
            <p>
              This removes the vocabulary item and its learning cards.
              Historical reviews and presentations remain. Archive the item to
              preserve its cards.
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
                disabled={deletion() !== initial.term}
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
            disabled={busy()}
            onClick={() => setConfirm(true)}
          >
            Delete item
          </button>
        </Show>
        <span class="spacer" />
        <button type="button" disabled={busy()} onClick={close}>
          Cancel
        </button>
        <button class="primary" type="submit" disabled={busy()}>
          {busy() ? "Saving…" : isNew ? "Add word" : "Save changes"}
        </button>
      </footer>
    </form>
  );
}
