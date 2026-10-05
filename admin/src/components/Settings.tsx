import { createMemo, createSignal, For, onCleanup, onMount, Show } from "solid-js";
import { APIError, type API } from "../api";
import { settingGroups, settingsDraft, validateSettings, type SettingsDraft, type SettingsSnapshot } from "../algorithmSettings";
import type { LeaveGuard } from "../vocabularyDraft";
import Modal from "./Modal";

export default function Settings(props: { api: API; registerGuard: (guard: LeaveGuard | undefined) => void }) {
  const [snapshot, setSnapshot] = createSignal<SettingsSnapshot>();
  const [draft, setDraft] = createSignal<SettingsDraft>({});
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const [notice, setNotice] = createSignal("");
  const [conflict, setConflict] = createSignal(false);
  const [pendingLeave, setPendingLeave] = createSignal<{ leave: () => void; cancel?: () => void }>();
  const validated = createMemo(() => validateSettings(draft()));
  const dirty = createMemo(() => !!snapshot() && JSON.stringify(draft()) !== JSON.stringify(settingsDraft(snapshot()!.values)));
  let active = true;
  const requestLeave: LeaveGuard = (leave, cancel) => {
    if (busy()) { cancel?.(); return; }
    if (!dirty()) { leave(); return; }
    const pending = { leave, cancel };
    setPendingLeave(pending);
    return () => { if (pendingLeave() === pending) setPendingLeave(undefined); };
  };
  const keepEditing = () => {
    const pending = pendingLeave();
    setPendingLeave(undefined);
    pending?.cancel?.();
  };
  props.registerGuard(requestLeave);
  const beforeUnload = (event: BeforeUnloadEvent) => {
    if (!dirty() && !busy()) return;
    event.preventDefault();
    event.returnValue = "";
  };
  window.addEventListener("beforeunload", beforeUnload);
  onCleanup(() => {
    active = false;
    props.registerGuard(undefined);
    window.removeEventListener("beforeunload", beforeUnload);
  });
  const accept = (loaded: SettingsSnapshot) => {
    setSnapshot(loaded);
    setDraft(settingsDraft(loaded.values));
    setConflict(false);
  };
  const load = async () => {
    if (busy()) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const loaded = await props.api<SettingsSnapshot>("/settings");
      if (active) accept(loaded);
    } catch (e) {
      if (active) setError(e instanceof Error ? e.message : String(e));
    } finally { if (active) setBusy(false); }
  };
  onMount(() => void load());
  const save = async (event: SubmitEvent) => {
    event.preventDefault();
    if (busy() || conflict() || !snapshot() || !dirty() || Object.keys(validated().errors).length) return;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const loaded = await props.api<SettingsSnapshot>("/settings", {
        method: "PUT",
        body: JSON.stringify({ values: validated().values, expectedRevision: snapshot()!.revision }),
      });
      if (active) {
        accept(loaded);
        setNotice("Settings saved. Future operations use these values; existing schedules are unchanged.");
      }
    } catch (e) {
      if (!active) return;
      setConflict(e instanceof APIError && e.status === 409);
      setError(e instanceof Error ? e.message : String(e));
    } finally { if (active) setBusy(false); }
  };
  return <section class="settings-page" aria-labelledby="settings-heading">
    <header>
      <h2 id="settings-heading">Algorithm settings</h2>
      <p>Owner-specific settings take effect without a restart for future reviews, selections and reinforcement. Saving does not reschedule existing cards or rewrite past ratings.</p>
      <p>Review ratings store the final grade. FSRS model weights remain fixed, short-term scheduling is enabled, and interval fuzz is disabled.</p>
    </header>
    <Show when={error()}><div class="alert error" role="alert">{error()}
      <Show when={!snapshot()}><button disabled={busy()} onClick={() => void load()}>Retry loading</button></Show>
    </div></Show>
    <Show when={conflict()}><div class="alert error" role="alert">
      <p>Settings changed elsewhere. Your draft is preserved and has not overwritten the newer settings. Reload the latest settings to review them before editing again.</p>
      <button disabled={busy()} onClick={() => requestLeave(() => void load())}>Reload latest settings</button>
    </div></Show>
    <Show when={notice()}><div class="alert success" role="status">{notice()}</div></Show>
    <Show when={busy() && !snapshot()}><div class="loading" role="status">Loading settings…</div></Show>
    <Show when={snapshot()}>
      <form onSubmit={save} noValidate aria-busy={busy()}>
        <For each={settingGroups}>{group => <fieldset class="settings-group" disabled={busy()}>
          <legend>{group.title}</legend>
          <p>{group.help}</p>
          <div class="settings-grid"><For each={group.fields}>{field => <div class="settings-field">
            <label for={`setting-${field.key}`}>{field.label} <span class="muted">({field.unit})</span></label>
            <Show when={field.options} fallback={
            <input id={`setting-${field.key}`} type={field.list ? "text" : "number"}
              min={field.min} max={field.max} step={field.integer ? 1 : "any"}
              inputmode={field.list ? "text" : "decimal"} value={draft()[field.key] ?? ""}
              aria-describedby={`setting-${field.key}-help${validated().errors[field.key] ? ` setting-${field.key}-error` : ""}`}
              aria-invalid={!!validated().errors[field.key]}
              onInput={event => { setDraft({ ...draft(), [field.key]: event.currentTarget.value }); setNotice(""); }} />
            }>
              <select id={`setting-${field.key}`} value={draft()[field.key] ?? ""}
                aria-describedby={`setting-${field.key}-help${validated().errors[field.key] ? ` setting-${field.key}-error` : ""}`}
                aria-invalid={!!validated().errors[field.key]}
                onChange={event => { setDraft({ ...draft(), [field.key]: event.currentTarget.value }); setNotice(""); }}>
                <For each={field.options}>{option => <option value={option.value}>{option.label}</option>}</For>
              </select>
            </Show>
            <small id={`setting-${field.key}-help`}>{field.help}</small>
            <Show when={validated().errors[field.key]}><small class="settings-error" id={`setting-${field.key}-error`}>{validated().errors[field.key]}</small></Show>
          </div>}</For></div>
        </fieldset>}</For>
        <footer class="settings-actions">
          <span role="status">{busy() ? "Saving…" : dirty() ? "Unsaved changes" : `Saved · revision ${snapshot()!.revision}`}</span>
          <button type="button" disabled={busy()} onClick={() => { setDraft(settingsDraft(snapshot()!.defaults)); setNotice("Defaults loaded into your draft. Save changes to apply them."); }}>Restore defaults</button>
          <button type="button" disabled={busy() || !dirty()} onClick={() => { setDraft(settingsDraft(snapshot()!.values)); setNotice(""); setError(""); }}>Discard changes</button>
          <button type="submit" class="primary" disabled={busy() || conflict() || !dirty() || Object.keys(validated().errors).length > 0}>Save changes</button>
        </footer>
      </form>
    </Show>
    <Show when={pendingLeave()}><Modal title="Unsaved settings changes" close={keepEditing}>
      <div class="dialog-body"><p>Your draft has not been saved. Keep editing, or discard it to continue.</p></div>
      <footer class="dialog-footer"><button autofocus onClick={keepEditing}>Keep editing</button><button class="danger" onClick={() => { const pending = pendingLeave(); setPendingLeave(undefined); pending?.leave(); }}>Discard draft and continue</button></footer>
    </Modal></Show>
  </section>;
}
