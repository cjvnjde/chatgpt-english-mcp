export const labels: Record<string, string> = {
  vocabulary_items: "Vocabulary",
  review_attempts: "Review attempts",
  learning_presentations: "Presentations",
  learning_cards: "Learning cards",
  dictionary_snapshots: "Dictionary cache",
  schema_migrations: "Migrations",
  usefulness_inference_state: "Usefulness inference",
  sqlite_sequence: "SQLite sequences",
  _term: "Word",
  custom_description: "Description",
  learning_status: "Status",
  effective_rating: "Effective rating",
};
export function label(value: string) {
  return (
    labels[value] ||
    value.replaceAll("_", " ").replace(/^./, (c) => c.toUpperCase())
  );
}
export function display(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
export function pretty(value: unknown): string {
  if (typeof value === "string") {
    try {
      return JSON.stringify(JSON.parse(value), null, 2);
    } catch {
      return value;
    }
  }
  return JSON.stringify(value, null, 2) ?? "null";
}
export function date(value: unknown): string {
  if (!value) return "—";
  const parsed = new Date(String(value));
  return Number.isNaN(parsed.getTime())
    ? String(value)
    : parsed.toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      });
}
export function download(name: string, value: unknown) {
  const url = URL.createObjectURL(
    new Blob([JSON.stringify(value, null, 2)], { type: "application/json" }),
  );
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = name;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
