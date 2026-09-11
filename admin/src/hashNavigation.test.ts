import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createHashNavigation } from "./hashNavigation.ts";
import type { LeaveGuard } from "./vocabularyDraft.ts";

function fixture() {
  const entries = [
    {
      hash: "#view=vocabulary_items",
      state: { preserved: true } as Record<string, unknown>,
    },
  ];
  let index = 0;
  let displayed = entries[0].hash;
  let guard: LeaveGuard = (leave) => leave();
  let onHash = () => {};
  const parse = (url: string | URL) => {
    const parsed = new URL(url, "https://example.test");
    assert.equal(parsed.pathname, "/admin/");
    assert.equal(parsed.search, "?test=1");
    return parsed.hash;
  };
  const browser = {
    location: {
      pathname: "/admin/",
      search: "?test=1",
      get hash() {
        return entries[index].hash;
      },
    },
    history: {
      get state() {
        return entries[index].state;
      },
      replaceState(
        state: Record<string, unknown>,
        _unused: string,
        url?: string | URL | null,
      ) {
        entries[index] = {
          state,
          hash: url == null ? entries[index].hash : parse(url),
        };
      },
      pushState(
        state: Record<string, unknown>,
        _unused: string,
        url?: string | URL | null,
      ) {
        entries.splice(index + 1, entries.length, { state, hash: parse(url!) });
        index++;
      },
      go(delta: number) {
        index += delta;
        onHash();
      },
    },
  };
  const navigation = createHashNavigation(
    browser,
    (leave, cancel) => guard(leave, cancel),
    (hash) => {
      displayed = hash;
    },
  );
  onHash = navigation.onHash;
  navigation.navigate("#view=review_attempts");
  navigation.navigate("#view=analytics");
  return {
    browser,
    entries,
    navigation,
    displayed: () => displayed,
    guard: (next: LeaveGuard) => {
      guard = next;
    },
  };
}

test("Back and Forward traverse existing entries without pushing or dropping history", () => {
  const h = fixture();
  const original = structuredClone(h.entries);
  for (const [delta, hash] of [
    [-1, "#view=review_attempts"],
    [-1, "#view=vocabulary_items"],
    [1, "#view=review_attempts"],
    [1, "#view=analytics"],
  ] as const) {
    h.browser.history.go(delta);
    assert.equal(h.displayed(), hash);
    assert.equal(h.browser.location.hash, hash);
    assert.deepEqual(h.entries, original);
  }
});

test("cancelled dirty Back preserves the draft and every history entry, then discard can traverse", () => {
  const h = fixture();
  const original = structuredClone(h.entries);
  let discard: (() => void) | undefined;
  let keepEditing: (() => void) | undefined;
  h.guard((leave, cancel) => {
    discard = leave;
    keepEditing = cancel;
  });
  h.browser.history.go(-1);
  assert.equal(h.displayed(), "#view=analytics");
  keepEditing!();
  assert.equal(h.displayed(), "#view=analytics");
  assert.equal(h.browser.location.hash, "#view=analytics");
  assert.deepEqual(h.entries, original);
  h.browser.history.go(-1);
  discard!();
  assert.equal(h.displayed(), "#view=review_attempts");
  assert.equal(h.browser.location.hash, "#view=review_attempts");
  assert.deepEqual(h.entries, original);
  h.guard((leave) => leave());
  h.browser.history.go(-1);
  assert.equal(h.displayed(), "#view=vocabulary_items");
  h.browser.history.go(1);
  assert.equal(h.displayed(), "#view=review_attempts");
});

test("superseded discard and cancel decisions cannot move history or corrupt the current route", () => {
  for (const decision of ["discard", "cancel"]) {
    const h = fixture();
    h.navigation.navigate("#view=suggestions");
    h.browser.history.go(-1); // C, with a forward D entry.
    const original = structuredClone(h.entries);
    let discard: (() => void) | undefined;
    let cancel: (() => void) | undefined;
    let confirmationVisible = false;
    h.guard((leave, stay) => {
      discard = leave;
      cancel = stay;
      confirmationVisible = true;
      return () => {
        confirmationVisible = false;
      };
    });
    h.browser.history.go(-1); // Dirty C -> B, pending confirmation.
    h.browser.history.go(1); // Return to displayed C before deciding.
    assert.equal(confirmationVisible, false);
    (decision === "discard" ? discard : cancel)!();
    assert.equal(h.browser.location.hash, "#view=analytics");
    assert.equal(h.displayed(), "#view=analytics");
    assert.deepEqual(h.entries, original);
    h.guard((leave) => leave());
    h.navigation.navigate("#view=review_attempts");
    assert.equal(h.browser.location.hash, "#view=review_attempts");
    assert.equal(h.displayed(), "#view=review_attempts");
  }
});

test("busy traversal is cancelled, matching hashes do not request leave, and same-view navigation does not push", () => {
  const h = fixture();
  const original = structuredClone(h.entries);
  h.guard((_leave, cancel) => cancel?.());
  h.browser.history.go(-1);
  assert.equal(h.browser.location.hash, "#view=analytics");
  assert.deepEqual(h.entries, original);
  h.guard(() => assert.fail("matching hash requested leave"));
  h.navigation.onHash();
  h.guard((leave) => leave());
  h.navigation.navigate("#view=analytics");
  assert.deepEqual(h.entries, original);
});
