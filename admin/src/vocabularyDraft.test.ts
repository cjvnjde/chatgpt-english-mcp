import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
  draftDirty,
  mergeDraft,
  resolveDraftConflict,
  vocabularyChanges,
  vocabularyDraft,
  vocabularyPatch,
} from "./vocabularyDraft.ts";
import type { Vocabulary } from "./types.ts";

const item: Vocabulary = {
  itemId: "word-one",
  revision: 4,
  term: "bank",
  normalizedTerm: "bank",
  context: "river edge",
  status: "new",
  usefulness: "high",
  personalInterest: "normal",
  customDescription: "Original meaning",
  notes: ["Original note"],
  examples: [],
  tags: ["nature"],
  descriptionSource: {
    title: "Original source",
    url: "https://example.com/old",
  },
  createdAt: "2026-09-01",
  updatedAt: "2026-09-01",
};

test("interest-only edits do not replace usefulness or unchanged metadata", () => {
  const base = vocabularyDraft(item);
  assert.deepEqual(
    vocabularyPatch(base, { ...base, personalInterest: "low" }, item.revision),
    {
      personalInterest: "low",
      expectedRevision: 4,
    },
  );
  const created = vocabularyChanges(
    base,
    { ...base, personalInterest: "high" },
    true,
  );
  assert.equal(created.personalInterest, "high");
  assert.equal("usefulness" in created, false);
});

test("reverting list edits clears dirty state and does not send replacement lists", () => {
  const base = vocabularyDraft(item);
  const edited = { ...base, notes: ["Changed note"] };
  assert.equal(draftDirty(base, edited), true);
  const reverted = { ...edited, notes: [...base.notes] };
  assert.equal(draftDirty(base, reverted), false);
  assert.deepEqual(vocabularyChanges(base, reverted), {});
  assert.equal(draftDirty(base, { ...base, notes: [...base.notes, ""] }), true);
  assert.deepEqual(
    vocabularyChanges(base, { ...base, notes: [...base.notes, ""] }),
    {},
  );
});

test("three-way merge keeps unrelated fields but conflicts on the description/source tuple", () => {
  const base = vocabularyDraft(item);
  const local = { ...base, notes: ["My note"], sourceTitle: "My attribution" };
  const remote = vocabularyDraft({
    ...item,
    revision: 5,
    status: "learning",
    personalInterest: "high",
    descriptionSource: {
      title: "Original source",
      url: "https://example.com/new",
    },
  });
  const merged = mergeDraft(base, local, remote);
  assert.deepEqual(merged.conflicts, [
    {
      field: "description",
      remote: {
        customDescription: "Original meaning",
        sourceTitle: "Original source",
        sourceURL: "https://example.com/new",
      },
    },
  ]);
  assert.equal(merged.draft.status, "learning");
  assert.equal(merged.draft.personalInterest, "high");
  assert.deepEqual(vocabularyPatch(remote, merged.draft, 5), {
    notes: ["My note"],
    descriptionSource: {
      title: "My attribution",
      url: "https://example.com/old",
    },
    expectedRevision: 5,
  });
  assert.deepEqual(local.notes, ["My note"]);
  assert.equal(local.sourceURL, "https://example.com/old");
});

test("description and attribution can only resolve together", () => {
  const base = vocabularyDraft(item);
  const local = {
    ...base,
    sourceTitle: "Local source",
    notes: ["Keep my note"],
  };
  const remote = { ...base, customDescription: "Remote meaning" };
  const merged = mergeDraft(base, local, remote);
  assert.equal(merged.conflicts.length, 1);
  assert.equal(merged.conflicts[0].field, "description");
  const keepLocal = resolveDraftConflict(
    merged.draft,
    merged.conflicts[0],
    false,
  );
  assert.equal(keepLocal.customDescription, base.customDescription);
  assert.equal(keepLocal.sourceTitle, "Local source");
  const keepRemote = resolveDraftConflict(
    merged.draft,
    merged.conflicts[0],
    true,
  );
  assert.equal(keepRemote.customDescription, "Remote meaning");
  assert.equal(keepRemote.sourceTitle, base.sourceTitle);
  assert.deepEqual(keepRemote.notes, ["Keep my note"]);
});

test("a one-sided or identical description/source update merges without a conflict", () => {
  const base = vocabularyDraft(item);
  const updated = {
    ...base,
    customDescription: "Updated",
    sourceTitle: "New source",
    sourceURL: "",
  };
  for (const [local, remote] of [
    [base, updated],
    [updated, base],
    [updated, updated],
  ]) {
    const merged = mergeDraft(base, local, remote);
    assert.deepEqual(merged.conflicts, []);
    assert.deepEqual(merged.draft, updated);
  }
});

test("same-field conflicts retain the draft until an explicit local or remote resolution", () => {
  const base = vocabularyDraft(item);
  const local = {
    ...base,
    notes: ["My note"],
    customDescription: "Shared update",
  };
  const remote = {
    ...base,
    notes: ["Remote note"],
    customDescription: "Shared update",
  };
  const merged = mergeDraft(base, local, remote);
  assert.deepEqual(merged.conflicts, [
    { field: "notes", remote: ["Remote note"] },
  ]);
  assert.deepEqual(merged.draft.notes, ["My note"]);
  assert.deepEqual(vocabularyChanges(remote, merged.draft), {
    notes: ["My note"],
  });
  const keepRemote = { ...merged.draft, notes: remote.notes };
  assert.deepEqual(vocabularyChanges(remote, keepRemote), {});
  assert.equal(draftDirty(remote, keepRemote), false);
});

test("a hint edit always needs explicit resolution after a stale revision because computed usefulness hides hint changes", () => {
  const base = vocabularyDraft(item);
  const local = { ...base, usefulness: "low" };
  const remote = vocabularyDraft({ ...item, revision: 5, tags: ["new tag"] });
  const merged = mergeDraft(base, local, remote);
  assert.deepEqual(merged.conflicts, [{ field: "usefulness", remote: "" }]);
  assert.equal(merged.draft.usefulness, "low");
  assert.deepEqual(
    vocabularyChanges(remote, { ...merged.draft, usefulness: "" }),
    {},
  );
});

test("a second concurrent change conflicts with the merged baseline rather than replaying an old payload", () => {
  const base = vocabularyDraft(item);
  const local = {
    ...base,
    personalInterest: "high" as const,
    notes: ["My note"],
  };
  const remote = { ...base, status: "learning" };
  const first = mergeDraft(base, local, remote);
  const newer = { ...remote, notes: ["Another writer's note"] };
  const second = mergeDraft(remote, first.draft, newer);
  assert.deepEqual(second.conflicts, [
    { field: "notes", remote: ["Another writer's note"] },
  ]);
  assert.equal(second.draft.personalInterest, "high");
  assert.equal(second.draft.status, "learning");
  assert.deepEqual(second.draft.notes, ["My note"]);
});

test("missing or invalid loaded revisions cannot produce an unguarded patch", () => {
  const base = vocabularyDraft(item);
  for (const revision of [undefined, 0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
    assert.throws(
      () =>
        vocabularyPatch(
          base,
          { ...base, personalInterest: "high" },
          revision as number,
        ),
      /valid edit revision/,
    );
  }
});
