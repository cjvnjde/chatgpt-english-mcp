export function dateFilterColumn(columns: string[]) {
  // Keep precedence aligned with AdminRows in internal/storage/admin.go.
  return [
    "reviewed_at",
    "shown_at",
    "created_at",
    "fetched_at",
    "applied_at",
  ].find((column) => columns.includes(column));
}
export function filterOptions(column: string): string[] {
  if (column === "learning_status")
    return ["new", "learning", "learned", "archived"];
  if (["rating", "effective_rating", "last_rating"].includes(column))
    return ["again", "hard", "good", "easy"];
  if (["usefulness", "personal_interest"].includes(column))
    return ["low", "normal", "high"];
  return [];
}
export function quickFilterColumn(table: string) {
  return table === "vocabulary_items"
    ? "learning_status"
    : table === "review_attempts"
      ? "rating"
      : "";
}
