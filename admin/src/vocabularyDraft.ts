import type { Vocabulary } from "./types";

export type Draft = {
  term: string;
  context: string;
  status: string;
  usefulness: string;
  personalInterest: Vocabulary["personalInterest"];
  customDescription: string;
  notes: string[];
  examples: string[];
  tags: string[];
  sourceTitle: string;
  sourceURL: string;
};
export const fieldLabels: Record<keyof Draft, string> = {
  term: "Word or expression",
  context: "Context / meaning",
  status: "Learning status",
  usefulness: "Usefulness hint",
  personalInterest: "Personal interest",
  customDescription: "Description",
  notes: "Notes",
  examples: "Examples",
  tags: "Tags",
  sourceTitle: "Description source title",
  sourceURL: "Description source URL",
};
const fields = Object.keys(fieldLabels) as (keyof Draft)[];
const same = (a: Draft[keyof Draft], b: Draft[keyof Draft]) =>
  Array.isArray(a) && Array.isArray(b)
    ? a.length === b.length && a.every((value, index) => value === b[index])
    : a === b;

export function vocabularyDraft(item: Vocabulary): Draft {
  return {
    term: item.term,
    context: item.context || "",
    status: item.status,
    // The API returns computed usefulness, not the original hint. Empty means
    // preserve that hint; never infer it from the computed value or a table row.
    usefulness: "",
    personalInterest: item.personalInterest,
    customDescription: item.customDescription || "",
    notes: item.notes,
    examples: item.examples,
    tags: item.tags,
    sourceTitle: item.descriptionSource?.title || "",
    sourceURL: item.descriptionSource?.url || "",
  };
}

export function draftDirty(base: Draft, draft: Draft): boolean {
  return fields.some((field) => !same(base[field], draft[field]));
}

export function vocabularyChanges(base: Draft, draft: Draft, isNew = false) {
  const changes: Record<string, unknown> = {};
  for (const field of [
    "status",
    "personalInterest",
    "customDescription",
    "notes",
    "examples",
    "tags",
  ] as const) {
    const value = draft[field];
    const normalized = Array.isArray(value)
      ? value.filter((entry) => entry.trim())
      : value;
    if (isNew || !same(base[field], normalized)) changes[field] = normalized;
  }
  if (
    isNew ||
    draft.sourceTitle !== base.sourceTitle ||
    draft.sourceURL !== base.sourceURL
  ) {
    changes.descriptionSource = {
      title: draft.sourceTitle,
      url: draft.sourceURL,
    };
  }
  if (draft.usefulness && draft.usefulness !== base.usefulness)
    changes.usefulness = draft.usefulness;
  if (isNew) {
    changes.term = draft.term;
    changes.context = draft.context;
  }
  return changes;
}

export function vocabularyPatch(base: Draft, draft: Draft, revision: number) {
  if (!Number.isSafeInteger(revision) || revision < 1)
    throw new Error("Reload this item to obtain a valid edit revision.");
  return { ...vocabularyChanges(base, draft), expectedRevision: revision };
}

export type DraftConflict = { field: keyof Draft; remote: Draft[keyof Draft] };
export function mergeDraft(
  base: Draft,
  local: Draft,
  remote: Draft,
): { draft: Draft; conflicts: DraftConflict[] } {
  const draft = { ...remote };
  const conflicts: DraftConflict[] = [];
  for (const field of fields) {
    if (same(base[field], local[field])) continue;
    Object.assign(draft, { [field]: local[field] });
    // A changed hint cannot be compared to the API's computed usefulness. Ask
    // explicitly rather than assuming an unseen remote hint stayed unchanged.
    if (
      field === "usefulness" ||
      (!same(base[field], remote[field]) && !same(local[field], remote[field]))
    ) {
      conflicts.push({ field, remote: remote[field] });
    }
  }
  return { draft, conflicts };
}

export type LeaveGuard = (leave: () => void) => void;
